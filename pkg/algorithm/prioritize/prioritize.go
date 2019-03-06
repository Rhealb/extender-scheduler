package prioritize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/emicklei/go-restful"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/api"
)

const (
	prioritiesPrefix = "priorities"
)

type Interface interface {
	Name() string
	Ready() bool
	Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error
	NodesScoring(pod *v1.Pod, nodes []v1.Node) (*schedulerapi.HostPriorityList, error)
}

type Prioritize struct {
	Interface
}

func (p Prioritize) Handler(args schedulerapi.ExtenderArgs) (*schedulerapi.HostPriorityList, error) {
	return p.NodesScoring(args.Pod, args.Nodes.Items)
}

var prioritizeList []*Prioritize
var prioritizeMu sync.Mutex
var inited bool

func Regist(p *Prioritize) error {
	if p.Name() == "" {
		return fmt.Errorf("Prioritize name should not be empty")
	}
	prioritizeMu.Lock()
	defer prioritizeMu.Unlock()
	if inited {
		fmt.Errorf("please regist before init")
	}
	for _, existPrioritize := range prioritizeList {
		if existPrioritize.Name() == p.Name() {
			return fmt.Errorf("Prioritize %s is registed", p.Name)
		}
	}
	prioritizeList = append(prioritizeList, p)
	return nil
}

func Ready() bool {
	prioritizeMu.Lock()
	defer prioritizeMu.Unlock()
	if inited == false {
		return false
	}
	for _, p := range prioritizeList {
		if p.Ready() == false {
			return false
		}
	}
	return true
}

func Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	prioritizeMu.Lock()
	defer prioritizeMu.Unlock()
	for _, p := range prioritizeList {
		if err := p.Init(clientset, informerFactory); err != nil {
			return fmt.Errorf("init prioritize %s error:%v", p.Name(), err)
		}
	}
	inited = true
	return nil
}

func prioritieRoute(prioritize *Prioritize) restful.RouteFunction {
	return func(request *restful.Request, response *restful.Response) {
		if request.Request.Body == nil {
			http.Error(response, "Please send a request body", 400)
			return
		}
		var buf bytes.Buffer
		body := io.TeeReader(request.Request.Body, &buf)

		var extenderArgs schedulerapi.ExtenderArgs
		var hostPriorityList *schedulerapi.HostPriorityList

		if err := json.NewDecoder(body).Decode(&extenderArgs); err != nil {
			panic(err)
		}

		if list, err := prioritize.Handler(extenderArgs); err != nil {
			panic(err)
		} else {
			hostPriorityList = list
		}

		if resultBody, err := json.Marshal(hostPriorityList); err != nil {
			panic(err)
		} else {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusOK)
			response.Write(resultBody)
		}
	}
}

func InstallHttpServer(wsContainer *restful.Container, apiPrefix string) error {
	prioritizeMu.Lock()
	defer prioritizeMu.Unlock()
	if inited == false {
		return fmt.Errorf("prioritzes are not inited")
	}
	for _, p := range prioritizeList {
		ws := new(restful.WebService)
		ws.Path(fmt.Sprintf("/%s/%s/%s", apiPrefix, prioritiesPrefix, p.Name())).Consumes("*/*").Produces(restful.MIME_JSON)
		ws.Route(ws.POST("/").To(prioritieRoute(p)))
		wsContainer.Add(ws)
	}
	return nil
}
