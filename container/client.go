package container

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

const (
	defaultStopSignal = "SIGTERM"
)

// A Client is the interface through which watchtower interacts with the
// Docker API.
type Client interface {
	ListContainers(Filter) ([]Container, error)
	GetContainer(containerID string) (Container, error)
	StopContainer(Container, time.Duration) error
	StartContainer(Container) (string, error)
	RenameContainer(Container, string) error
	IsContainerStale(Container) (bool, error)
	RemoveImage(Container) error
	ExecuteCommand(containerID string, command string) error
}

// NewClient returns a new Client instance which can be used to interact with
// the Docker API.
// The client reads its configuration from the following environment variables:
//  * DOCKER_HOST			the docker-engine host to send api requests to
//  * DOCKER_TLS_VERIFY		whether to verify tls certificates
//  * DOCKER_API_VERSION	the minimum docker api version to work with
func NewClient(pullImages bool) Client {
	cli, err := dockerclient.NewEnvClient()

	if err != nil {
		log.Fatalf("Error instantiating Docker client: %s", err)
	}

	return dockerClient{api: cli, pullImages: pullImages}
}

type dockerClient struct {
	api        *dockerclient.Client
	pullImages bool
}

func (client dockerClient) ListContainers(fn Filter) ([]Container, error) {
	cs := []Container{}
	bg := context.Background()

	log.Debug("Retrieving running containers")

	runningContainers, err := client.api.ContainerList(
		bg,
		types.ContainerListOptions{})

	if err != nil {
		return nil, err
	}

	for _, runningContainer := range runningContainers {
		container, err := client.GetContainer(runningContainer.ID)
		if err != nil {
			return nil, err
		}

		if fn(container) {
			cs = append(cs, container)
		}
	}

	return cs, nil
}

func (client dockerClient) GetContainer(containerID string) (Container, error) {
	bg := context.Background()

	containerInfo, err := client.api.ContainerInspect(bg, containerID)
	if err != nil {
		return Container{}, err
	}

	imageInfo, _, err := client.api.ImageInspectWithRaw(bg, containerInfo.Image)
	if err != nil {
		return Container{}, err
	}

	container := Container{containerInfo: &containerInfo, imageInfo: &imageInfo}
	return container, nil
}

func (client dockerClient) StopContainer(c Container, timeout time.Duration) error {
	bg := context.Background()
	signal := c.StopSignal()
	if signal == "" {
		signal = defaultStopSignal
	}

	log.Infof("Stopping %s (%s) with %s", c.Name(), c.ID(), signal)

	if err := client.api.ContainerKill(bg, c.ID(), signal); err != nil {
		return err
	}

	// Wait for container to exit, but proceed anyway after the timeout elapses
	client.waitForStop(c, timeout)

	if c.containerInfo.HostConfig.AutoRemove {
		log.Debugf("AutoRemove container %s, skipping ContainerRemove call.", c.ID())
	} else {
		log.Debugf("Removing container %s", c.ID())

		if err := client.api.ContainerRemove(bg, c.ID(), types.ContainerRemoveOptions{Force: true, RemoveVolumes: false}); err != nil {
			return err
		}
	}

	// Wait for container to be removed. In this case an error is a good thing
	if err := client.waitForStop(c, timeout); err == nil {
		return fmt.Errorf("Container %s (%s) could not be removed", c.Name(), c.ID())
	}

	return nil
}

func (client dockerClient) StartContainer(c Container) (string, error) {
	bg := context.Background()
	config := c.runtimeConfig()
	hostConfig := c.hostConfig()
	networkConfig := &network.NetworkingConfig{EndpointsConfig: c.containerInfo.NetworkSettings.Networks}
	// simpleNetworkConfig is a networkConfig with only 1 network.
	// see: https://github.com/docker/docker/issues/29265
	simpleNetworkConfig := func() *network.NetworkingConfig {
		oneEndpoint := make(map[string]*network.EndpointSettings)
		for k, v := range networkConfig.EndpointsConfig {
			oneEndpoint[k] = v
			// we only need 1
			break
		}
		return &network.NetworkingConfig{EndpointsConfig: oneEndpoint}
	}()

	name := c.Name()

	log.Infof("Creating %s", name)
	creation, err := client.api.ContainerCreate(bg, config, hostConfig, simpleNetworkConfig, name)
	if err != nil {
		return "", err
	}

	if !(hostConfig.NetworkMode.IsHost()) {

		for k := range simpleNetworkConfig.EndpointsConfig {
			err = client.api.NetworkDisconnect(bg, k, creation.ID, true)
			if err != nil {
				return "", err
			}
		}

		for k, v := range networkConfig.EndpointsConfig {
			err = client.api.NetworkConnect(bg, k, creation.ID, v)
			if err != nil {
				return "", err
			}
		}

	}

	log.Debugf("Starting container %s (%s)", name, creation.ID)

	err = client.api.ContainerStart(bg, creation.ID, types.ContainerStartOptions{})
	if err != nil {
		return "", err
	}

	return creation.ID, nil

}

func (client dockerClient) RenameContainer(c Container, newName string) error {
	bg := context.Background()
	log.Debugf("Renaming container %s (%s) to %s", c.Name(), c.ID(), newName)
	return client.api.ContainerRename(bg, c.ID(), newName)
}

func (client dockerClient) IsContainerStale(c Container) (bool, error) {
	bg := context.Background()
	oldImageInfo := c.imageInfo
	imageName := c.ImageName()

	if client.pullImages {
		log.Debugf("Pulling %s for %s", imageName, c.Name())

		var opts types.ImagePullOptions // ImagePullOptions can take a RegistryAuth arg to authenticate against a private registry
		auth, err := EncodedAuth(imageName)
		if err != nil {
			log.Debugf("Error loading authentication credentials %s", err)
			return false, err
		} else if auth == "" {
			log.Debugf("No authentication credentials found for %s", imageName)
			opts = types.ImagePullOptions{} // empty/no auth credentials
		} else {
			opts = types.ImagePullOptions{RegistryAuth: auth, PrivilegeFunc: DefaultAuthHandler}
		}

		response, err := client.api.ImagePull(bg, imageName, opts)
		if err != nil {
			log.Debugf("Error pulling image %s, %s", imageName, err)
			return false, err
		}
		defer response.Close()

		// the pull request will be aborted prematurely unless the response is read
		_, err = ioutil.ReadAll(response)
	}

	newImageInfo, _, err := client.api.ImageInspectWithRaw(bg, imageName)
	if err != nil {
		return false, err
	}

	if newImageInfo.ID != oldImageInfo.ID {
		log.Infof("Found new %s image (%s)", imageName, newImageInfo.ID)
		return true, nil
	}

	log.Debugf("No new images found for %s", c.Name())
	return false, nil
}

func (client dockerClient) ExecuteCommand(containerID string, command string) error {
	bg := context.Background()

	// Create the exec
	execConfig := types.ExecConfig{
		Tty:          true,
		AttachStderr: true,
		AttachStdout: true,
		Detach:       false,
		Cmd:          []string{"sh", "-c", command},
	}

	exec, err := client.api.ContainerExecCreate(bg, containerID, execConfig)
	if err != nil {
		return err
	}

	response, attachErr := client.api.ContainerExecAttach(bg, exec.ID, types.ExecConfig{
		Tty:          true,
		AttachStderr: true,
		AttachStdout: true,
		Detach:       false,
	})
	if attachErr != nil {
		log.Errorf("Failed to extract command exec logs: %v", attachErr)
	}

	// Run the exec
	execStartCheck := types.ExecStartCheck{Detach: false, Tty: true}
	err = client.api.ContainerExecStart(bg, exec.ID, execStartCheck)
	if err != nil {
		return err
	}

	var execOutput string
	if attachErr == nil {
		defer response.Close()
		var writer bytes.Buffer
		written, err := writer.ReadFrom(response.Reader)
		if err != nil {
			log.Error(err)
		} else if written > 0 {
			execOutput = strings.TrimSpace(writer.String())
		}
	}

	// Inspect the exec to get the exit code and print a message if the
	// exit code is not success.
	execInspect, err := client.api.ContainerExecInspect(bg, exec.ID)
	if err != nil {
		return err
	}

	if execInspect.ExitCode > 0 {
		log.Errorf("Command exited with code %v.", execInspect.ExitCode)
		log.Error(execOutput)
	} else {
		if len(execOutput) > 0 {
			log.Infof("Command output:\n%v", execOutput)
		}
	}

	return nil
}

func (client dockerClient) RemoveImage(c Container) error {
	imageID := c.ImageID()
	log.Infof("Removing image %s", imageID)
	_, err := client.api.ImageRemove(context.Background(), imageID, types.ImageRemoveOptions{Force: true})
	return err
}

func (client dockerClient) waitForStop(c Container, waitTime time.Duration) error {
	bg := context.Background()
	timeout := time.After(waitTime)

	for {
		select {
		case <-timeout:
			return nil
		default:
			if ci, err := client.api.ContainerInspect(bg, c.ID()); err != nil {
				return err
			} else if !ci.State.Running {
				return nil
			}
		}

		time.Sleep(1 * time.Second)
	}
}
