package types

// Report contains reports for all the containers processed during a session
type Report interface {
	Scanned() []ContainerReport
	Updated() []ContainerReport
	Failed() []ContainerReport
	Skipped() []ContainerReport
	Stale() []ContainerReport
	Fresh() []ContainerReport
}

// ContainerReport represents a container that was included in watchtower session
type ContainerReport interface {
	ID() string
	Name() string
	OldImageID() string
	NewImageID() string
	ImageName() string
	Error() string
	State() string
}
