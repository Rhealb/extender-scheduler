package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"k8s-plugins/extender-scheduler/pkg/algorithm/predicate"
	"k8s-plugins/extender-scheduler/pkg/algorithm/prioritize"

	"github.com/emicklei/go-restful"
	"github.com/golang/glog"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	cache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	apiPrefix = "scheduler"
	version   = "v0.1.0"
)

var (
	metricAddress               = flag.String("metric-address", ":8001", "The address to expose Prometheus metrics.")
	address                     = flag.String("address", ":8000", "The address to expose server.")
	nsNodeSelectorAddress       = flag.String("nsselect-server-address", ":8001", "The address to expose nsnodeselector server.")
	nsNodeSelectorCertFile      = flag.String("nsselect-server-cert-file", "", "The nsnodeselector server cert file.")
	nsNodeSelectorKeyFile       = flag.String("nsselect-server-key-file", "", "The nsnodeselector server key file.")
	nsNodeSelectorBasicAuthFile = flag.String("nsselect-server-basic-auth-file", "", "The nsnodeselector server basic auth file.")
	kubeConfig                  = flag.String("kubeconfig", "", "kube config file path")
	runMode                     = flag.String("runmode", "all", "[all, scheduleronly, backendonly] are valid")
)

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func getClientset() (*kubernetes.Clientset, error) {
	config, errConfig := buildConfig(*kubeConfig)
	if errConfig != nil {
		return nil, errConfig
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}
func initAll(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	if errInit := predicate.Init(clientset, informerFactory); errInit != nil {
		return errInit
	}
	if errInit := prioritize.Init(clientset, informerFactory); errInit != nil {
		return errInit
	}
	return nil
}

func waitReady(informerFactory informers.SharedInformerFactory, stopCh <-chan struct{}) error {
	informerFactory.Start(stopCh)
	if !cache.WaitForCacheSync(stopCh, predicate.Ready) {
		fmt.Println("Cannot sync predicates caches")
		return fmt.Errorf("wait timeout")
	}
	if !cache.WaitForCacheSync(stopCh, prioritize.Ready) {
		fmt.Println("Cannot sync prioritizes caches")
		return fmt.Errorf("wait timeout")
	}
	return nil
}

func installHttpServer(wsContainer *restful.Container) error {
	if errInstall := predicate.InstallHttpServer(wsContainer, apiPrefix); errInstall != nil {
		return fmt.Errorf("predicates install err:%v", errInstall)
	}
	if errInstall := prioritize.InstallHttpServer(wsContainer, apiPrefix); errInstall != nil {
		return fmt.Errorf("prioritizes install err:%v", errInstall)
	}
	return installCommonHttpServer(wsContainer)
}
func installCommonHttpServer(wsContainer *restful.Container) error {
	wsVersion := new(restful.WebService)
	wsVersion.Path("/version").Consumes("*/*").Produces(restful.MIME_JSON)
	wsVersion.Route(wsVersion.GET("/").To(func(request *restful.Request, response *restful.Response) {
		response.Write([]byte(version))
	}))
	wsHealth := new(restful.WebService)
	wsHealth.Path("/health").Consumes("*/*").Produces(restful.MIME_JSON)
	wsHealth.Route(wsHealth.GET("/").To(func(request *restful.Request, response *restful.Response) {
		response.Write([]byte("OK"))
	}))
	wsContainer.Add(wsVersion)
	wsContainer.Add(wsHealth)

	return nil
}

func main() {
	flag.Parse()
	defer glog.Flush()

	if *runMode != "all" && *runMode != "scheduleronly" && *runMode != "backendonly" {
		glog.Fatalf("runMode [%s] is not support", *runMode)
	}
	clientset, errGet := getClientset()
	if errGet != nil {
		glog.Error(errGet.Error())
		os.Exit(1)
	}
	stopCh := make(chan struct{})
	if *runMode == "all" || *runMode == "backendonly" {
		predicate.StartPolicyHttpServer(clientset, 10*time.Second, *nsNodeSelectorAddress, *nsNodeSelectorCertFile, *nsNodeSelectorKeyFile, *nsNodeSelectorBasicAuthFile)
	}
	if *runMode == "backendonly" {
		<-stopCh
		glog.Errorf("should not to here")
		os.Exit(1)
	}
	mux := http.NewServeMux()
	var wsContainer *restful.Container = restful.NewContainer()
	wsContainer.Router(restful.CurlyRouter{})

	wsContainer.ServeMux = mux

	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	glog.Infof("start init all")
	if errInit := initAll(clientset, informerFactory); errInit != nil {
		glog.Errorf("initAll err:%v", errInit)
		os.Exit(2)
	}
	glog.Infof("start waitReady")
	if errWait := waitReady(informerFactory, stopCh); errWait != nil {
		glog.Errorf("waitReady err:%v", errWait)
		os.Exit(3)
	}
	glog.Infof("start installHttpServer")
	if errInstall := installHttpServer(wsContainer); errInstall != nil {
		glog.Errorf("install httpserver err:%v", errInstall)
		os.Exit(4)
	}
	glog.Infof("start server at: %s", *address)
	server := &http.Server{Addr: *address, Handler: wsContainer}
	err := server.ListenAndServe()
	glog.Errorf("server err:%v\n", err)
}
