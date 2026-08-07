package main

import (
	"log"
	"os"
)

type Request struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
		// CreationTimestamp is the resource's own creation time, always present on
		// the live object /kratix/input/object.yaml is rendered from (Kratix's
		// reader init container re-fetches the resource fresh on every pipeline
		// run). Used by pr-watcher as the wait-clock anchor for its timeout, since
		// it's the one timestamp guaranteed to survive across the separate Jobs
		// that Kratix creates for each retryAfter cycle — /kratix/metadata does not.
		CreationTimestamp string `yaml:"creationTimestamp"`
	} `yaml:"metadata"`
	Spec struct {
		AppName            string    `yaml:"appName"`
		DisplayName        string    `yaml:"displayName"`
		BusinessUnit       string    `yaml:"businessUnit"`
		Description        string    `yaml:"description"`
		ProductOwner       string    `yaml:"productOwner"`
		EngineeringManager string    `yaml:"engineeringManager"`
		TeamMembers        []string  `yaml:"teamMembers"`
		Environment        string    `yaml:"environment"`
		Location           string    `yaml:"location"`
		Resources          Resources `yaml:"resources"`
	} `yaml:"spec"`
}

type Resources struct {
	Namespace      bool `yaml:"namespace"`
	KeyVault       bool `yaml:"keyvault"`
	StorageAccount bool `yaml:"storageAccount"`
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
