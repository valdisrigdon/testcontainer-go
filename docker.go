package testcontainer

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainer-go/wait"
)

// RequestContainer is the input object used to get a running container.
type RequestContainer struct {
	Env          map[string]string
	ExportedPort []string
	Cmd          string
	RegistryCred string
	WaitingFor   wait.WaitStrategy
}

// Container is the struct used to represent a single container.
type Container struct {
	// Container ID from Docker
	ID string
	// Cache to retrieve container infromation without re-fetching them from dockerd
	raw *types.ContainerJSON
}

func (c *Container) LivenessCheckPorts(ctx context.Context) (nat.PortSet, error) {
	inspect, err := inspectContainer(ctx, c)
	if err != nil {
		return nil, err
	}
	return inspect.Config.ExposedPorts, nil
}

// Terminate is used to kill the container. It is usally triggered by as defer function.
func (c *Container) Terminate(ctx context.Context, t *testing.T) error {
	cli, err := client.NewEnvClient()
	if err != nil {
		t.Error(err)
		return err
	}
	return cli.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{
		Force: true,
	})
}

func inspectContainer(ctx context.Context, c *Container) (*types.ContainerJSON, error) {
	if c.raw != nil {
		return c.raw, nil
	}
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	inspect, err := cli.ContainerInspect(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	c.raw = &inspect
	return c.raw, nil
}

// GetIPAddress returns the ip address for the running container.
func (c *Container) GetIPAddress(ctx context.Context) (string, error) {
	inspect, err := inspectContainer(ctx, c)
	if err != nil {
		return "", err
	}
	return inspect.NetworkSettings.IPAddress, nil
}

// GetMappedPort returns PortBindings for the running Container.
func (c *Container) GetMappedPort(ctx context.Context, port int) (int, error) {
	var binding int
	inspect, err := inspectContainer(ctx, c)
	if err != nil {
		return binding, err
	}
	for key, val := range inspect.NetworkSettings.Ports {
		if key.Int() == port {
			log.Printf("Port %d -> %s", port, val[0].HostPort)
			return strconv.Atoi(val[0].HostPort)
		}
	}
	return binding, fmt.Errorf("Unable to find mapped port: %d", port)
}

// RunContainer takes a RequestContainer as input and it runs a container via the docker sdk
func RunContainer(ctx context.Context, containerImage string, input RequestContainer) (*Container, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	exposedPorts, portBindings, err := nat.ParsePortSpecs(input.ExportedPort)
	if err != nil {
		return nil, err
	}
	log.Printf("portBindings: %s", portBindings)

	env := []string{}
	for envKey, envVar := range input.Env {
		env = append(env, envKey+"="+envVar)
	}

	dockerInput := &container.Config{
		Image:        containerImage,
		Env:          env,
		ExposedPorts: exposedPorts,
	}

	if input.Cmd != "" {
		dockerInput.Cmd = strings.Split(input.Cmd, " ")
	}

	pullOpt := types.ImagePullOptions{}
	if input.RegistryCred != "" {
		pullOpt.RegistryAuth = input.RegistryCred
	}
	pull, err := cli.ImagePull(ctx, dockerInput.Image, pullOpt)
	if err != nil {
		return nil, err
	}
	defer pull.Close()

	// download of docker image finishes at EOF of the pull request
	_, err = ioutil.ReadAll(pull)
	if err != nil {
		return nil, err
	}

	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
	}

	resp, err := cli.ContainerCreate(ctx, dockerInput, hostConfig, nil, "")
	if err != nil {
		return nil, err
	}
	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return nil, err
	}
	containerInstance := &Container{
		ID: resp.ID,
	}

	// if a WaitStrategy has been specified, wait before returning
	if input.WaitingFor != nil {
		if err := input.WaitingFor.WaitUntilReady(ctx, containerInstance); err != nil {
			// return containerInstance for termination
			return containerInstance, err
		}
	}
	return containerInstance, nil
}
