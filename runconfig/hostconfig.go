package runconfig

import (
	"github.com/dotcloud/docker/engine"
	"github.com/dotcloud/docker/nat"
)

type HostConfig struct {
	Binds           []string
	ContainerIDFile string
	LxcConf         []KeyValuePair
	Privileged      bool
	PortBindings    nat.PortMap
	Links           []string
	PublishAllPorts bool
}

type KeyValuePair struct {
	Key   string
	Value string
}

// This is used by the create command when you want to both set the
// Config and the HostConfig in the same call
type ConfigAndHostConfig struct {
	Config
	HostConfig HostConfig
}

func MergeConfigs(config *Config, hostConfig *HostConfig) *ConfigAndHostConfig {
	return &ConfigAndHostConfig{
		*config,
		*hostConfig,
	}
}

func ContainerHostConfigFromJob(job *engine.Job) *HostConfig {
	if job.EnvExists("HostConfig") {
		hostConfig := HostConfig{}
		job.GetenvJson("HostConfig", &hostConfig)
		return &hostConfig
	}

	hostConfig := &HostConfig{
		ContainerIDFile: job.Getenv("ContainerIDFile"),
		Privileged:      job.GetenvBool("Privileged"),
		PublishAllPorts: job.GetenvBool("PublishAllPorts"),
	}
	job.GetenvJson("LxcConf", &hostConfig.LxcConf)
	job.GetenvJson("PortBindings", &hostConfig.PortBindings)
	if Binds := job.GetenvList("Binds"); Binds != nil {
		hostConfig.Binds = Binds
	}
	if Links := job.GetenvList("Links"); Links != nil {
		hostConfig.Links = Links
	}

	return hostConfig
}
