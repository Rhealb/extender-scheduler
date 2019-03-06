package predicate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/emicklei/go-restful"
	"k8s.io/client-go/kubernetes"
	//	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/api"
)

const (
	predicatesPrefix = "predicates"
)

type PredicateError struct {
	name string
	desc string
}

func newPredicateError(name, desc string) *PredicateError {
	return &PredicateError{name: name, desc: desc}
}
func (e *PredicateError) Error() string {
	return fmt.Sprintf("Predicate %s failed because %s", e.name, e.desc)
}

type Interface interface {
	Name() string
	Ready() bool
	Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error
	PodMatchNode(pod *v1.Pod, node *v1.Node) (bool, error)
}

type Predicate struct {
	Interface
}

func (p Predicate) Handler(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {
	pod := args.Pod
	canSchedule := make([]v1.Node, 0, len(args.Nodes.Items))
	canNotSchedule := make(map[string]string)

	for i := range args.Nodes.Items {
		node := &args.Nodes.Items[i]
		result, err := p.PodMatchNode(pod, node)
		if err != nil {
			canNotSchedule[node.Name] = err.Error()
		} else {
			if result {
				canSchedule = append(canSchedule, *node)
			}
		}
	}

	result := schedulerapi.ExtenderFilterResult{
		Nodes: &v1.NodeList{
			Items: canSchedule,
		},
		FailedNodes: canNotSchedule,
		Error:       "",
	}

	return &result
}

var predicateList []*Predicate
var predicateMu sync.Mutex
var inited bool

func Regist(p *Predicate) error {
	if p.Name() == "" {
		return fmt.Errorf("Predicate name should not be empty")
	}
	predicateMu.Lock()
	defer predicateMu.Unlock()
	if inited {
		fmt.Errorf("please regist before init")
	}
	for _, existPredicate := range predicateList {
		if existPredicate.Name() == p.Name() {
			return fmt.Errorf("Predicate %s is registed", p.Name)
		}
	}
	predicateList = append(predicateList, p)
	return nil
}

func Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	predicateMu.Lock()
	defer predicateMu.Unlock()
	for _, p := range predicateList {
		if err := p.Init(clientset, informerFactory); err != nil {
			return fmt.Errorf("init predicate %s error:%v", p.Name(), err)
		}
	}
	inited = true
	return nil
}

func predicateRoute(predicate *Predicate) restful.RouteFunction {
	return func(request *restful.Request, response *restful.Response) {
		if request.Request.Body == nil {
			http.Error(response, "Please send a request body", 400)
			return
		}
		var buf bytes.Buffer
		body := io.TeeReader(request.Request.Body, &buf)

		var extenderArgs schedulerapi.ExtenderArgs
		var extenderFilterResult *schedulerapi.ExtenderFilterResult

		if err := json.NewDecoder(body).Decode(&extenderArgs); err != nil {
			extenderFilterResult = &schedulerapi.ExtenderFilterResult{
				Nodes:       nil,
				FailedNodes: nil,
				Error:       err.Error(),
			}
		} else {
			extenderFilterResult = predicate.Handler(extenderArgs)
		}

		if resultBody, err := json.Marshal(extenderFilterResult); err != nil {
			panic(err)
		} else {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusOK)
			response.Write(resultBody)
		}
	}
}

func Ready() bool {
	predicateMu.Lock()
	defer predicateMu.Unlock()
	if inited == false {
		return false
	}
	for _, p := range predicateList {
		if p.Ready() == false {
			return false
		}
	}
	return true
}

func InstallHttpServer(wsContainer *restful.Container, apiPrefix string) error {
	predicateMu.Lock()
	defer predicateMu.Unlock()
	if inited == false {
		return fmt.Errorf("predicates are not inited")
	}
	for _, p := range predicateList {
		ws := new(restful.WebService)
		ws.Path(fmt.Sprintf("/%s/%s/%s", apiPrefix, predicatesPrefix, p.Name())).Consumes("*/*").Produces(restful.MIME_JSON)
		ws.Route(ws.POST("/").To(predicateRoute(p)))
		wsContainer.Add(ws)
	}
	return nil
}
