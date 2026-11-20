package snap

import (
	"fmt"
	"strings"
)

// serviceName infers the name of the snapctl daemon from the service name.
// if the serviceName is the snap name `k8s` (=referes to all services) it will return it as is.
func serviceNameForInstance(instanceName string, name string) string {
	inst := instanceName
	if inst == "" {
		inst = "k8s"
	}
	if name == inst || strings.HasPrefix(name, inst+".") {
		return name
	}
	if name == "k8s" {
		return inst
	}
	return fmt.Sprintf("%s.%s", inst, strings.TrimPrefix(name, "k8s."))
}

func serviceName(name string) string {
	return serviceNameForInstance("k8s", name)
}
