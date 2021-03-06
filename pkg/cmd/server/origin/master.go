package origin

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	etcdclient "github.com/coreos/go-etcd/etcd"
	"github.com/elazarl/go-bindata-assetfs"
	restful "github.com/emicklei/go-restful"
	"github.com/emicklei/go-restful/swagger"
	"github.com/golang/glog"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/admission"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/apiserver"
	kclient "github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	kmaster "github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/admission/admit"

	"github.com/openshift/origin/pkg/api/latest"
	"github.com/openshift/origin/pkg/api/v1beta1"
	"github.com/openshift/origin/pkg/assets"
	"github.com/openshift/origin/pkg/auth/authenticator"
	authcontext "github.com/openshift/origin/pkg/auth/context"
	"github.com/openshift/origin/pkg/authorization/authorizer"
	buildclient "github.com/openshift/origin/pkg/build/client"
	buildcontrollerfactory "github.com/openshift/origin/pkg/build/controller/factory"
	buildstrategy "github.com/openshift/origin/pkg/build/controller/strategy"
	buildregistry "github.com/openshift/origin/pkg/build/registry/build"
	buildconfigregistry "github.com/openshift/origin/pkg/build/registry/buildconfig"
	buildlogregistry "github.com/openshift/origin/pkg/build/registry/buildlog"
	buildetcd "github.com/openshift/origin/pkg/build/registry/etcd"
	"github.com/openshift/origin/pkg/build/webhook"
	"github.com/openshift/origin/pkg/build/webhook/generic"
	"github.com/openshift/origin/pkg/build/webhook/github"
	osclient "github.com/openshift/origin/pkg/client"
	cmdutil "github.com/openshift/origin/pkg/cmd/util"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	deploycontrollerfactory "github.com/openshift/origin/pkg/deploy/controller/factory"
	deployconfiggenerator "github.com/openshift/origin/pkg/deploy/generator"
	deployregistry "github.com/openshift/origin/pkg/deploy/registry/deploy"
	deployconfigregistry "github.com/openshift/origin/pkg/deploy/registry/deployconfig"
	deployetcd "github.com/openshift/origin/pkg/deploy/registry/etcd"
	deployrollback "github.com/openshift/origin/pkg/deploy/rollback"
	imageetcd "github.com/openshift/origin/pkg/image/registry/etcd"
	"github.com/openshift/origin/pkg/image/registry/image"
	"github.com/openshift/origin/pkg/image/registry/imagerepository"
	"github.com/openshift/origin/pkg/image/registry/imagerepositorymapping"
	"github.com/openshift/origin/pkg/image/registry/imagerepositorytag"
	accesstokenregistry "github.com/openshift/origin/pkg/oauth/registry/accesstoken"
	authorizetokenregistry "github.com/openshift/origin/pkg/oauth/registry/authorizetoken"
	clientregistry "github.com/openshift/origin/pkg/oauth/registry/client"
	clientauthorizationregistry "github.com/openshift/origin/pkg/oauth/registry/clientauthorization"
	oauthetcd "github.com/openshift/origin/pkg/oauth/registry/etcd"
	projectetcd "github.com/openshift/origin/pkg/project/registry/etcd"
	projectregistry "github.com/openshift/origin/pkg/project/registry/project"
	routeetcd "github.com/openshift/origin/pkg/route/registry/etcd"
	routeregistry "github.com/openshift/origin/pkg/route/registry/route"
	"github.com/openshift/origin/pkg/service"
	templateregistry "github.com/openshift/origin/pkg/template/registry"
	"github.com/openshift/origin/pkg/user"
	useretcd "github.com/openshift/origin/pkg/user/registry/etcd"
	userregistry "github.com/openshift/origin/pkg/user/registry/user"
	"github.com/openshift/origin/pkg/user/registry/useridentitymapping"
	"github.com/openshift/origin/pkg/version"

	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
	authorizationetcd "github.com/openshift/origin/pkg/authorization/registry/etcd"
	policyregistry "github.com/openshift/origin/pkg/authorization/registry/policy"
	policybindingregistry "github.com/openshift/origin/pkg/authorization/registry/policybinding"
	roleregistry "github.com/openshift/origin/pkg/authorization/registry/role"
	rolebindingregistry "github.com/openshift/origin/pkg/authorization/registry/rolebinding"
)

const (
	OpenShiftAPIPrefix        = "/osapi"
	OpenShiftAPIPrefixV1Beta1 = OpenShiftAPIPrefix + "/v1beta1"
	swaggerAPIPrefix          = "/swaggerapi/"
)

// MasterConfig defines the required parameters for starting the OpenShift master
type MasterConfig struct {
	// host:port to bind master to
	MasterBindAddr string
	// host:port to bind asset server to
	AssetBindAddr string
	// url to access the master API on within the cluster
	MasterAddr string
	// url to access kubernetes API on within the cluster
	KubernetesAddr string
	// external clients may need to access APIs at different addresses than internal components do
	MasterPublicAddr     string
	KubernetesPublicAddr string
	AssetPublicAddr      string

	CORSAllowedOrigins []string
	Authenticator      authenticator.Request
	// TODO Have MasterConfig take a fully formed Authorizer
	MasterAuthorizationNamespace string

	EtcdHelper tools.EtcdHelper

	AdmissionControl admission.Interface

	// true if the system should use pullIfNotPresent for images (which means updates will not be fetched aggressively)
	UseLocalImages bool

	// a function that returns the appropriate image to use for a named component
	ImageFor func(component string) string

	TLS bool

	MasterCertFile string
	MasterKeyFile  string
	AssetCertFile  string
	AssetKeyFile   string

	// kubeClient is the client used to call Kubernetes APIs from system components, built from KubeClientConfig.
	// It should only be accessed via the *Client() helper methods.
	// To apply different access control to a system component, create a separate client/config specifically for that component.
	kubeClient *kclient.Client
	// KubeClientConfig is the client configuration used to call Kubernetes APIs from system components.
	// To apply different access control to a system component, create a client config specifically for that component.
	KubeClientConfig kclient.Config

	// osClient is the client used to call OpenShift APIs from system components, built from OSClientConfig.
	// It should only be accessed via the *Client() helper methods.
	// To apply different access control to a system component, create a separate client/config specifically for that component.
	osClient *osclient.Client
	// OSClientConfig is the client configuration used to call OpenShift APIs from system components
	// To apply different access control to a system component, create a client config specifically for that component.
	OSClientConfig kclient.Config

	// DeployerOSClientConfig is the client configuration used to call OpenShift APIs from launched deployer pods
	DeployerOSClientConfig kclient.Config

	// requestsToUsers is a shared auth context map
	requestsToUsers *authcontext.RequestContextMap
}

// APIInstaller installs additional API components into this server
type APIInstaller interface {
	// Returns an array of strings describing what was installed
	InstallAPI(*restful.Container) []string
}

// APIInstallFunc is a function for installing APIs
type APIInstallFunc func(*restful.Container) []string

// InstallAPI implements APIInstaller
func (fn APIInstallFunc) InstallAPI(container *restful.Container) []string {
	return fn(container)
}

func (c *MasterConfig) BuildClients() {
	kubeClient, err := kclient.New(&c.KubeClientConfig)
	if err != nil {
		glog.Fatalf("Unable to configure client: %v", err)
	}
	c.kubeClient = kubeClient

	osclient, err := osclient.New(&c.OSClientConfig)
	if err != nil {
		glog.Fatalf("Unable to configure client: %v", err)
	}
	c.osClient = osclient
}

// KubeClient returns the kubernetes client object
func (c *MasterConfig) KubeClient() *kclient.Client {
	return c.kubeClient
}

// DeploymentClient returns the deployment client object
func (c *MasterConfig) DeploymentClient() *kclient.Client {
	return c.kubeClient
}

// BuildLogClient returns the build log client object
func (c *MasterConfig) BuildLogClient() *kclient.Client {
	return c.kubeClient
}

// WebHookClient returns the webhook client object
func (c *MasterConfig) WebHookClient() *osclient.Client {
	return c.osClient
}

// BuildControllerClients returns the build controller client objects
func (c *MasterConfig) BuildControllerClients() (*osclient.Client, *kclient.Client) {
	return c.osClient, c.kubeClient
}

// ImageChangeControllerClient returns the openshift client object
func (c *MasterConfig) ImageChangeControllerClient() *osclient.Client {
	return c.osClient
}

// DeploymentControllerClients returns the deployment controller client object
func (c *MasterConfig) DeploymentControllerClients() (*osclient.Client, *kclient.Client) {
	return c.osClient, c.kubeClient
}

// DeployerClientConfig returns the client configuration a Deployer instance launched in a pod
// should use when making API calls.
func (c *MasterConfig) DeployerClientConfig() *kclient.Config {
	return &c.DeployerOSClientConfig
}

func (c *MasterConfig) DeploymentConfigControllerClients() (*osclient.Client, *kclient.Client) {
	return c.osClient, c.kubeClient
}
func (c *MasterConfig) DeploymentConfigChangeControllerClients() (*osclient.Client, *kclient.Client) {
	return c.osClient, c.kubeClient
}
func (c *MasterConfig) DeploymentImageChangeControllerClient() *osclient.Client {
	return c.osClient
}

func (c *MasterConfig) InstallProtectedAPI(container *restful.Container) []string {
	defaultRegistry := env("OPENSHIFT_DEFAULT_REGISTRY", "${DOCKER_REGISTRY_SERVICE_HOST}:${DOCKER_REGISTRY_SERVICE_PORT}")
	svcCache := service.NewServiceResolverCache(c.KubeClient().Services(api.NamespaceDefault).Get)
	defaultRegistryFunc, err := svcCache.Defer(defaultRegistry)
	if err != nil {
		glog.Fatalf("OPENSHIFT_DEFAULT_REGISTRY variable is invalid %q: %v", defaultRegistry, err)
	}

	buildEtcd := buildetcd.New(c.EtcdHelper)
	imageEtcd := imageetcd.New(c.EtcdHelper, imageetcd.DefaultRegistryFunc(defaultRegistryFunc))
	deployEtcd := deployetcd.New(c.EtcdHelper)
	routeEtcd := routeetcd.New(c.EtcdHelper)
	projectEtcd := projectetcd.New(c.EtcdHelper)
	userEtcd := useretcd.New(c.EtcdHelper, user.NewDefaultUserInitStrategy())
	oauthEtcd := oauthetcd.New(c.EtcdHelper)
	authorizationEtcd := authorizationetcd.New(c.EtcdHelper)

	// TODO: with sharding, this needs to be changed
	deployConfigGenerator := &deployconfiggenerator.DeploymentConfigGenerator{
		Client: deployconfiggenerator.Client{
			DCFn:   deployEtcd.GetDeploymentConfig,
			IRFn:   imageEtcd.GetImageRepository,
			LIRFn2: imageEtcd.ListImageRepositories,
		},
		Codec: latest.Codec,
	}
	_, kclient := c.DeploymentConfigControllerClients()
	deployRollback := &deployrollback.RollbackGenerator{}
	deployRollbackClient := deployrollback.Client{
		DCFn: deployEtcd.GetDeploymentConfig,
		RCFn: clientDeploymentInterface{kclient}.GetDeployment,
		GRFn: deployRollback.GenerateRollback,
	}

	// initialize OpenShift API
	storage := map[string]apiserver.RESTStorage{
		"builds":       buildregistry.NewREST(buildEtcd),
		"buildConfigs": buildconfigregistry.NewREST(buildEtcd),
		"buildLogs":    buildlogregistry.NewREST(buildEtcd, c.BuildLogClient()),

		"images":                  image.NewREST(imageEtcd),
		"imageRepositories":       imagerepository.NewREST(imageEtcd),
		"imageRepositoryMappings": imagerepositorymapping.NewREST(imageEtcd, imageEtcd),
		"imageRepositoryTags":     imagerepositorytag.NewREST(imageEtcd, imageEtcd),

		"deployments":               deployregistry.NewREST(deployEtcd),
		"deploymentConfigs":         deployconfigregistry.NewREST(deployEtcd),
		"generateDeploymentConfigs": deployconfiggenerator.NewREST(deployConfigGenerator, v1beta1.Codec),
		"deploymentConfigRollbacks": deployrollback.NewREST(deployRollbackClient, latest.Codec),

		"templateConfigs": templateregistry.NewREST(),

		"routes": routeregistry.NewREST(routeEtcd),

		"projects": projectregistry.NewREST(projectEtcd),

		"userIdentityMappings": useridentitymapping.NewREST(userEtcd),
		"users":                userregistry.NewREST(userEtcd),

		"oAuthAuthorizeTokens":      authorizetokenregistry.NewREST(oauthEtcd),
		"oAuthAccessTokens":         accesstokenregistry.NewREST(oauthEtcd),
		"oAuthClients":              clientregistry.NewREST(oauthEtcd),
		"oAuthClientAuthorizations": clientauthorizationregistry.NewREST(oauthEtcd),

		"policies":       policyregistry.NewREST(authorizationEtcd),
		"policyBindings": policybindingregistry.NewREST(authorizationEtcd),
		"roles":          roleregistry.NewREST(authorizationEtcd),
		"roleBindings":   rolebindingregistry.NewREST(authorizationEtcd, authorizationEtcd, userEtcd, c.MasterAuthorizationNamespace),
	}

	admissionControl := admit.NewAlwaysAdmit()

	if err := apiserver.NewAPIGroupVersion(storage, v1beta1.Codec, OpenShiftAPIPrefixV1Beta1, latest.SelfLinker, admissionControl, latest.RESTMapper).InstallREST(container, OpenShiftAPIPrefix, "v1beta1"); err != nil {
		glog.Fatalf("Unable to initialize API: %v", err)
	}

	var root *restful.WebService
	userRoutesChanged := 0
	for _, svc := range container.RegisteredWebServices() {
		switch svc.RootPath() {
		case "/":
			root = svc
		case OpenShiftAPIPrefixV1Beta1:
			svc.Doc("OpenShift REST API, version v1beta1").ApiVersion("v1beta1")

			// add the current user filter
			// TODO: factor this better
			filter := currentUserContextFilter(c.getRequestsToUsers())
			routes := svc.Routes()
			for i := range routes {
				route := &routes[i]
				if route.Method == "GET" && (route.Path == OpenShiftAPIPrefixV1Beta1+"/users/{name}") {
					route.Filters = append(route.Filters, filter)
					userRoutesChanged++
				}
			}
		}
	}
	if userRoutesChanged != 1 {
		glog.Fatalf("Could not find user route to install the current user filter.")
	}
	if root == nil {
		root = new(restful.WebService)
		container.Add(root)
	}
	initAPIVersionRoute(root, "v1beta1")

	return []string{
		fmt.Sprintf("Started OpenShift API at %%s%s", OpenShiftAPIPrefixV1Beta1),
	}
}

func (c *MasterConfig) InstallUnprotectedAPI(container *restful.Container) []string {
	bcClient, _ := c.BuildControllerClients()
	handler := webhook.NewController(
		buildclient.NewOSClientBuildConfigClient(bcClient),
		buildclient.NewOSClientBuildClient(bcClient),
		map[string]webhook.Plugin{
			"generic": generic.New(),
			"github":  github.New(),
		})

	// TODO: go-restfulize this
	prefix := OpenShiftAPIPrefixV1Beta1 + "/buildConfigHooks/"
	handler = http.StripPrefix(prefix, handler)
	container.Handle(prefix, handler)
	return []string{}
}

//initAPIVersionRoute initializes the osapi endpoint to behave similiar to the upstream api endpoint
func initAPIVersionRoute(root *restful.WebService, version string) {
	versionHandler := apiserver.APIVersionHandler(version)
	root.Route(root.GET(OpenShiftAPIPrefix).To(versionHandler).
		Doc("list supported server API versions").
		Produces(restful.MIME_JSON).
		Consumes(restful.MIME_JSON))
}

// Run launches the OpenShift master. It takes optional installers that may install additional endpoints into the server.
// All endpoints get configured CORS behavior
// Protected installers' endpoints are protected by API authentication and authorization.
// Unprotected installers' endpoints do not have any additional protection added.
func (c *MasterConfig) Run(protected []APIInstaller, unprotected []APIInstaller) {
	var extra []string

	c.ensureComponentAuthorizationRules()

	safe := kmaster.NewHandlerContainer(http.NewServeMux())
	open := kmaster.NewHandlerContainer(http.NewServeMux())

	// enforce authentication on protected endpoints
	protected = append(protected, APIInstallFunc(c.InstallProtectedAPI))
	for _, i := range protected {
		extra = append(extra, i.InstallAPI(safe)...)
	}
	handler := c.authorizationFilter(safe)
	handler = authenticationHandlerFilter(handler, c.Authenticator, c.getRequestsToUsers())

	// unprotected resources
	unprotected = append(unprotected, APIInstallFunc(c.InstallUnprotectedAPI))
	for _, i := range unprotected {
		extra = append(extra, i.InstallAPI(open)...)
	}
	open.Handle("/", handler)

	// install swagger
	swaggerConfig := swagger.Config{
		WebServices: append(safe.RegisteredWebServices(), open.RegisteredWebServices()...),
		ApiPath:     swaggerAPIPrefix,
	}
	swagger.RegisterSwaggerService(swaggerConfig, open)
	extra = append(extra, fmt.Sprintf("Started Swagger Schema API at %%s%s", swaggerAPIPrefix))

	handler = open

	// add CORS support
	if origins := c.ensureCORSAllowedOrigins(); len(origins) != 0 {
		handler = apiserver.CORS(handler, origins, nil, nil, "true")
	}

	server := &http.Server{
		Addr:           c.MasterBindAddr,
		Handler:        handler,
		ReadTimeout:    5 * time.Minute,
		WriteTimeout:   5 * time.Minute,
		MaxHeaderBytes: 1 << 20,
	}

	go util.Forever(func() {
		for _, s := range extra {
			glog.Infof(s, c.MasterAddr)
		}
		if c.TLS {
			server.TLSConfig = &tls.Config{
				// Change default from SSLv3 to TLSv1.0 (because of POODLE vulnerability)
				MinVersion: tls.VersionTLS10,
				// Populate PeerCertificates in requests, but don't reject connections without certificates
				// This allows certificates to be validated by authenticators, while still allowing other auth types
				ClientAuth: tls.RequestClientCert,
			}
			glog.Fatal(server.ListenAndServeTLS(c.MasterCertFile, c.MasterKeyFile))
		} else {
			glog.Fatal(server.ListenAndServe())
		}
	}, 0)

	// Attempt to verify the server came up for 20 seconds (100 tries * 100ms, 100ms timeout per try)
	cmdutil.WaitForSuccessfulDial("tcp", c.MasterBindAddr, 100*time.Millisecond, 100*time.Millisecond, 100)
}

// getRequestsToUsers returns the shared user context
func (c *MasterConfig) getRequestsToUsers() *authcontext.RequestContextMap {
	if c.requestsToUsers == nil {
		c.requestsToUsers = authcontext.NewRequestContextMap()
	}
	return c.requestsToUsers
}

// ensureComponentAuthorizationRules initializes the global policies
func (c *MasterConfig) ensureComponentAuthorizationRules() {
	registry := authorizationetcd.New(c.EtcdHelper)
	ctx := kapi.WithNamespace(kapi.NewContext(), c.MasterAuthorizationNamespace)

	if existing, err := registry.GetPolicy(ctx, authorizationapi.PolicyName); err == nil || strings.Contains(err.Error(), " not found") {
		if existing != nil && existing.Name == authorizationapi.PolicyName {
			return
		}

		bootstrapGlobalPolicy := authorizer.GetBootstrapPolicy(c.MasterAuthorizationNamespace)
		if err = registry.CreatePolicy(ctx, bootstrapGlobalPolicy); err != nil {
			glog.Errorf("Error creating policy: %v due to %v\n", bootstrapGlobalPolicy, err)
		}

	} else {
		glog.Errorf("Error getting policy: %v due to %v\n", authorizationapi.PolicyName, err)
	}

	if existing, err := registry.GetPolicyBinding(ctx, c.MasterAuthorizationNamespace); err == nil || strings.Contains(err.Error(), " not found") {
		if existing != nil && existing.Name == c.MasterAuthorizationNamespace {
			return
		}

		bootstrapGlobalPolicyBinding := authorizer.GetBootstrapPolicyBinding(c.MasterAuthorizationNamespace)
		if err = registry.CreatePolicyBinding(ctx, bootstrapGlobalPolicyBinding); err != nil {
			glog.Errorf("Error creating policy: %v due to %v\n", bootstrapGlobalPolicyBinding, err)
		}

	} else {
		glog.Errorf("Error getting policy: %v due to %v\n", c.MasterAuthorizationNamespace, err)
	}
}

// TODO Have MasterConfig take a fully formed Authorizer
func (c *MasterConfig) authorizationFilter(handler http.Handler) http.Handler {
	authorizationEtcd := authorizationetcd.New(c.EtcdHelper)
	authorizationAttributeBuilder := authorizer.NewAuthorizationAttributeBuilder(c.getRequestsToUsers())
	authz := authorizer.NewAuthorizer(c.MasterAuthorizationNamespace, authorizationEtcd, authorizationEtcd)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		attributes, err := authorizationAttributeBuilder.GetAttributes(req)
		// TODO: this significantly relaxes the authorization guarantees - however the unprotected resources need
		// to be clearly split out upstream in a way that we can detect.
		if err == authorizer.ErrNoStandardParts {
			glog.V(4).Infof("Allowing %q because it is not a recognized form", req.RequestURI)
			handler.ServeHTTP(w, req)
			return
		}
		if err != nil {
			// fail
			forbidden(err.Error(), w, req)
			return
		}
		if attributes == nil {
			// fail
			forbidden("No attributes", w, req)
			return
		}

		allowed, reason, err := authz.Authorize(attributes)
		if err != nil {
			// fail
			forbidden(err.Error(), w, req)
			return
		}
		if !allowed {
			forbidden(reason, w, req)
			return
		}

		handler.ServeHTTP(w, req)
	})
}

// forbidden renders a simple forbidden error
func forbidden(reason string, w http.ResponseWriter, req *http.Request) {
	glog.V(1).Infof("!!!!!!!!!!!! FORBIDDING because %v!\n", reason)
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, "Forbidden: %q %s", req.RequestURI, reason)
}

// RunAssetServer starts the asset server for the OpenShift UI.
func (c *MasterConfig) RunAssetServer() {
	// TODO use	version.Get().GitCommit as an etag cache header
	mux := http.NewServeMux()

	masterURL, err := url.Parse(c.MasterPublicAddr)
	if err != nil {
		glog.Fatalf("Error parsing master url: %v", err)
	}

	k8sURL, err := url.Parse(c.KubernetesPublicAddr)
	if err != nil {
		glog.Fatalf("Error parsing kubernetes url: %v", err)
	}

	config := assets.WebConsoleConfig{
		MasterAddr:        masterURL.Host,
		MasterPrefix:      OpenShiftAPIPrefix,
		KubernetesAddr:    k8sURL.Host,
		KubernetesPrefix:  "/api",
		OAuthAuthorizeURL: OpenShiftOAuthAuthorizeURL(masterURL.String()),
		OAuthRedirectBase: c.AssetPublicAddr,
		OAuthClientID:     OpenShiftWebConsoleClientID,
	}

	mux.Handle("/",
		// Gzip first so that inner handlers can react to the addition of the Vary header
		assets.GzipHandler(
			// Generated config.js can not be cached since it changes depending on startup options
			assets.GeneratedConfigHandler(
				config,
				// Cache control should happen after all Vary headers are added, but before
				// any asset related routing (HTML5ModeHandler and FileServer)
				assets.CacheControlHandler(
					version.Get().GitCommit,
					assets.HTML5ModeHandler(
						http.FileServer(
							&assetfs.AssetFS{
								assets.Asset,
								assets.AssetDir,
								"",
							},
						),
					),
				),
			),
		),
	)

	server := &http.Server{
		Addr:           c.AssetBindAddr,
		Handler:        mux,
		ReadTimeout:    5 * time.Minute,
		WriteTimeout:   5 * time.Minute,
		MaxHeaderBytes: 1 << 20,
	}

	go util.Forever(func() {
		if c.TLS {
			server.TLSConfig = &tls.Config{
				// Change default from SSLv3 to TLSv1.0 (because of POODLE vulnerability)
				MinVersion: tls.VersionTLS10,
				// Populate PeerCertificates in requests, but don't reject connections without certificates
				// This allows certificates to be validated by authenticators, while still allowing other auth types
				ClientAuth: tls.RequestClientCert,
			}
			glog.Infof("OpenShift UI listening at https://%s", c.AssetBindAddr)
			glog.Fatal(server.ListenAndServeTLS(c.AssetCertFile, c.AssetKeyFile))
		} else {
			glog.Infof("OpenShift UI listening at https://%s", c.AssetBindAddr)
			glog.Fatal(server.ListenAndServe())
		}
	}, 0)

	// Attempt to verify the server came up for 20 seconds (100 tries * 100ms, 100ms timeout per try)
	cmdutil.WaitForSuccessfulDial("tcp", c.AssetBindAddr, 100*time.Millisecond, 100*time.Millisecond, 100)

	glog.Infof("OpenShift UI available at %s", c.AssetPublicAddr)
}

// RunBuildController starts the build sync loop for builds and buildConfig processing.
func (c *MasterConfig) RunBuildController() {
	// initialize build controller
	dockerImage := c.ImageFor("docker-builder")
	stiImage := c.ImageFor("sti-builder")
	useLocalImages := c.UseLocalImages

	osclient, kclient := c.BuildControllerClients()
	factory := buildcontrollerfactory.BuildControllerFactory{
		OSClient:     osclient,
		KubeClient:   kclient,
		BuildUpdater: buildclient.NewOSClientBuildClient(osclient),
		DockerBuildStrategy: &buildstrategy.DockerBuildStrategy{
			Image:          dockerImage,
			UseLocalImages: useLocalImages,
			// TODO: this will be set to --storage-version (the internal schema we use)
			Codec: v1beta1.Codec,
		},
		STIBuildStrategy: &buildstrategy.STIBuildStrategy{
			Image:                stiImage,
			TempDirectoryCreator: buildstrategy.STITempDirectoryCreator,
			UseLocalImages:       useLocalImages,
			// TODO: this will be set to --storage-version (the internal schema we use)
			Codec: v1beta1.Codec,
		},
		CustomBuildStrategy: &buildstrategy.CustomBuildStrategy{
			UseLocalImages: useLocalImages,
			// TODO: this will be set to --storage-version (the internal schema we use)
			Codec: v1beta1.Codec,
		},
	}

	controller := factory.Create()
	controller.Run()
}

// RunDeploymentController starts the build image change trigger controller process.
func (c *MasterConfig) RunBuildImageChangeTriggerController() {
	bcClient, _ := c.BuildControllerClients()
	bcUpdater := buildclient.NewOSClientBuildConfigClient(bcClient)
	bCreator := buildclient.NewOSClientBuildClient(bcClient)
	factory := buildcontrollerfactory.ImageChangeControllerFactory{Client: bcClient, BuildCreator: bCreator, BuildConfigUpdater: bcUpdater}
	factory.Create().Run()
}

// RunDeploymentController starts the deployment controller process.
func (c *MasterConfig) RunDeploymentController() {
	osclient, kclient := c.DeploymentControllerClients()
	factory := deploycontrollerfactory.DeploymentControllerFactory{
		Client:     osclient,
		KubeClient: kclient,
		Codec:      latest.Codec,
		Environment: []api.EnvVar{
			{Name: "KUBERNETES_MASTER", Value: c.MasterAddr},
			{Name: "OPENSHIFT_MASTER", Value: c.MasterAddr},
		},
		UseLocalImages:        c.UseLocalImages,
		RecreateStrategyImage: c.ImageFor("deployer"),
	}

	envvars := clientcmd.EnvVarsFromConfig(c.DeployerClientConfig())
	factory.Environment = append(factory.Environment, envvars...)

	controller := factory.Create()
	controller.Run()
}

func (c *MasterConfig) RunDeploymentConfigController() {
	osclient, kclient := c.DeploymentConfigControllerClients()
	factory := deploycontrollerfactory.DeploymentConfigControllerFactory{
		Client:     osclient,
		KubeClient: kclient,
		Codec:      latest.Codec,
	}
	controller := factory.Create()
	controller.Run()
}

func (c *MasterConfig) RunDeploymentConfigChangeController() {
	osclient, kclient := c.DeploymentConfigChangeControllerClients()
	factory := deploycontrollerfactory.DeploymentConfigChangeControllerFactory{
		Client:     osclient,
		KubeClient: kclient,
		Codec:      latest.Codec,
	}
	controller := factory.Create()
	controller.Run()
}

func (c *MasterConfig) RunDeploymentImageChangeTriggerController() {
	osclient := c.DeploymentImageChangeControllerClient()
	factory := deploycontrollerfactory.ImageChangeControllerFactory{Client: osclient}
	controller := factory.Create()
	controller.Run()
}

// ensureCORSAllowedOrigins takes a string list of origins and attempts to covert them to CORS origin
// regexes, or exits if it cannot.
func (c *MasterConfig) ensureCORSAllowedOrigins() []*regexp.Regexp {
	if len(c.CORSAllowedOrigins) == 0 {
		return []*regexp.Regexp{}
	}
	allowedOriginRegexps, err := util.CompileRegexps(util.StringList(c.CORSAllowedOrigins))
	if err != nil {
		glog.Fatalf("Invalid --cors-allowed-origins: %v", err)
	}
	return allowedOriginRegexps
}

// NewEtcdHelper returns an EtcdHelper for the provided arguments or an error if the version
// is incorrect.
func NewEtcdHelper(version string, client *etcdclient.Client) (helper tools.EtcdHelper, err error) {
	if len(version) == 0 {
		version = latest.Version
	}
	interfaces, err := latest.InterfacesFor(version)
	if err != nil {
		return helper, err
	}
	return tools.EtcdHelper{client, interfaces.Codec, tools.RuntimeVersionAdapter{interfaces.MetadataAccessor}}, nil
}

// env returns an environment variable, or the defaultValue if it is not set.
func env(key string, defaultValue string) string {
	val := os.Getenv(key)
	if len(val) == 0 {
		return defaultValue
	}
	return val
}

type clientDeploymentInterface struct {
	KubeClient kclient.Interface
}

func (c clientDeploymentInterface) GetDeployment(ctx api.Context, name string) (*api.ReplicationController, error) {
	return c.KubeClient.ReplicationControllers(api.Namespace(ctx)).Get(name)
}
