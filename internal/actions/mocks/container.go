package mocks

import (
	"fmt"
	"github.com/containrrr/watchtower/pkg/container"
	wt "github.com/containrrr/watchtower/pkg/types"
	"github.com/docker/docker/api/types"
	dockerContainer "github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"strconv"
	"strings"
	"time"
)

// CreateMockContainer creates a container substitute valid for testing
func CreateMockContainer(id string, name string, image string, created time.Time) container.Container {
	content := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:      id,
			Image:   image,
			Name:    name,
			Created: created.String(),
			HostConfig: &dockerContainer.HostConfig{
				PortBindings: map[nat.Port][]nat.PortBinding{},
			},
		},
		Config: &dockerContainer.Config{
			Image:        image,
			Labels:       make(map[string]string),
			ExposedPorts: map[nat.Port]struct{}{},
		},
	}
	return *container.NewContainer(
		&content,
		&types.ImageInspect{
			ID: image,
			RepoDigests: []string{
				image,
			},
		},
	)
}

// CreateMockContainerWithImageInfo should only be used for testing
func CreateMockContainerWithImageInfo(id string, name string, image string, created time.Time, imageInfo types.ImageInspect) container.Container {
	content := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:      id,
			Image:   image,
			Name:    name,
			Created: created.String(),
		},
		Config: &dockerContainer.Config{
			Image:  image,
			Labels: make(map[string]string),
		},
	}
	return *container.NewContainer(
		&content,
		&imageInfo,
	)
}

// CreateMockContainerWithDigest should only be used for testing
func CreateMockContainerWithDigest(id string, name string, image string, created time.Time, digest string) container.Container {
	c := CreateMockContainer(id, name, image, created)
	c.ImageInfo().RepoDigests = []string{digest}
	return c
}

// CreateMockContainerWithConfig creates a container substitute valid for testing
func CreateMockContainerWithConfig(id string, name string, image string, running bool, restarting bool, created time.Time, config *dockerContainer.Config) container.Container {
	content := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:    id,
			Image: image,
			Name:  name,
			State: &types.ContainerState{
				Running:    running,
				Restarting: restarting,
			},
			Created: created.String(),
			HostConfig: &dockerContainer.HostConfig{
				PortBindings: map[nat.Port][]nat.PortBinding{},
			},
		},
		Config: config,
	}
	return *container.NewContainer(
		&content,
		&types.ImageInspect{
			ID: image,
		},
	)
}

func CreateContainerForProgress(index int, idPrefix int, nameFormat string) (container.Container, wt.ImageID) {
	indexStr := strconv.Itoa(idPrefix + index)
	mockID := indexStr + strings.Repeat("0", 61-len(indexStr))
	contID := "c79" + mockID
	contName := fmt.Sprintf(nameFormat, index+1)
	oldImgID := "01d" + mockID
	newImgID := "d0a" + mockID
	imageName := fmt.Sprintf("mock/%s:latest", contName)
	config := &dockerContainer.Config{
		Image: imageName,
	}
	c := CreateMockContainerWithConfig(contID, contName, oldImgID, true, false, time.Now(), config)
	return c, wt.ImageID(newImgID)
}
