package templaterouter

import (
	routeapi "github.com/openshift/origin/pkg/route/api"
)

// ServiceUnit is an encapsulation of a service, the endpoints that back that service, and the routes
// that point to the service.  This is the data that drives the creation of the router configuration files
type ServiceUnit struct {
	// Name corresponds to a service name & namespace.  Uniquely identifies the ServiceUnit
	Name string
	// EndpointTable are endpoints that back the service, this translates into a final backend implementation for routers
	// keyed by IP:port for easy access
	EndpointTable map[string]Endpoint
	// ServiceAliasConfigs is a collection of unique routes that support this service, keyed by host + path
	ServiceAliasConfigs map[string]ServiceAliasConfig
}

// ServiceAliasConfig is a route for a service.  Uniquely identified by host + path.
type ServiceAliasConfig struct {
	// Required host name ie www.example.com
	Host string
	// An optional path.  Ie. www.example.com/myservice where "myservice" is the path
	Path string
	// Termination policy for this backend, drives the mapping files and router configuration
	TLSTermination routeapi.TLSTerminationType
	// Certificates used for securing this backend.  Keyed by the cert id
	Certificates map[string]Certificate
}

// Certificate represents a pub/private key pair.  It is identified by ID which is set to indicate if this is
// a client or ca certificate (see router.go).  A CA certificate will not have a PrivateKey set.
type Certificate struct {
	ID         string
	Contents   string
	PrivateKey string
}

// Endpoint is an internal representation of a k8s endpoint.
type Endpoint struct {
	ID   string
	IP   string
	Port string
}
