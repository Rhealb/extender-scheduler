package predicate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emicklei/go-restful"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/plugin/pkg/authenticator/password/passwordfile"
	"k8s.io/apiserver/plugin/pkg/authenticator/request/basicauth"
	"k8s.io/client-go/kubernetes"
)

type SchedulerConfig struct {
	client                            *kubernetes.Clientset
	nsNodeSelectorConfigMap           *v1.ConfigMap
	configMapTimeOut                  time.Duration
	nsNodeSelectorConfigMapUpdateTime time.Time
}

type ReturnMsg struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data string `json:"data"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type NsNodeSelectorConfigRet struct {
	Match        []KeyValue `json:"match"`
	MustMatch    []KeyValue `json:"mustMatch"`
	NotMatch     []KeyValue `json:"notMatch"`
	MustNotMatch []KeyValue `json:"mustNotMatch"`
}

func SetNsNodeSelectorConfigMapNsConfig(cm *v1.ConfigMap, nsc NsConfig) error {
	if cm == nil {
		return fmt.Errorf("cm == nil")
	}
	buf, _ := json.MarshalIndent(nsc, " ", "  ")
	cm.Data[nsnodeselector_itemname] = string(buf)
	return nil
}

func CreateOrUpdateSchedulerPolicyConfigMap(client *kubernetes.Clientset, updateCm *v1.ConfigMap) (*v1.ConfigMap, error) {
	curCM, err := client.CoreV1().ConfigMaps(updateCm.Namespace).Get(updateCm.Name, meta_v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return client.CoreV1().ConfigMaps(updateCm.Namespace).Create(updateCm)
		}
		glog.Errorf("CreateOrUpdateSchedulerPolicyConfigMap %s:%s , err:%v", nsnodeselector_configmap_ns, nsnodeselector_configmap_name, err)
		return nil, err
	} else {
		curCM.Data = updateCm.Data
		return client.CoreV1().ConfigMaps(curCM.Namespace).Update(curCM)
	}
}

func DeletePod(client *kubernetes.Clientset, namespace, name string) error {
	var GracePeriodSeconds int64 = 0
	return client.CoreV1().Pods(namespace).Delete(name, &meta_v1.DeleteOptions{GracePeriodSeconds: &GracePeriodSeconds})
}

func GetPods(client *kubernetes.Clientset, namespace string) ([]*v1.Pod, error) {
	pods, err := client.CoreV1().Pods(namespace).List(meta_v1.ListOptions{})
	if err != nil {
		return []*v1.Pod{}, err
	}
	ret := make([]*v1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		ret = append(ret, &pods.Items[i])
	}
	return ret, nil
}

func GetNamespaces(client *kubernetes.Clientset) ([]*v1.Namespace, error) {
	nsList, err := client.CoreV1().Namespaces().List(meta_v1.ListOptions{})
	if err != nil {
		return []*v1.Namespace{}, err
	}
	ret := make([]*v1.Namespace, 0, len(nsList.Items))
	for i := range nsList.Items {
		ret = append(ret, &nsList.Items[i])
	}
	return ret, nil
}

func GetNodes(client *kubernetes.Clientset) ([]*v1.Node, error) {
	nodeList, err := client.CoreV1().Nodes().List(meta_v1.ListOptions{})
	if err != nil {
		return []*v1.Node{}, err
	}
	ret := make([]*v1.Node, 0, len(nodeList.Items))
	for i := range nodeList.Items {
		ret = append(ret, &nodeList.Items[i])
	}
	return ret, nil
}

func (sc *SchedulerConfig) getSelectorConfigMap(client *kubernetes.Clientset) *v1.ConfigMap {
	if nsNodeSelectorConfigMap != nil && time.Since(nsNodeSelectorConfigMapUpdateTime) <= nsNodeSelectorConfigMapCacheTimeOut {
		return nsNodeSelectorConfigMap
	}
	cm, err := client.CoreV1().ConfigMaps(nsnodeselector_configmap_ns).Get(nsnodeselector_configmap_name, meta_v1.GetOptions{})
	if err != nil || cm == nil {
		return &v1.ConfigMap{
			ObjectMeta: meta_v1.ObjectMeta{
				Namespace: nsnodeselector_configmap_ns,
				Name:      nsnodeselector_configmap_name,
			},
			Data: map[string]string{},
		}
	}
	nsNodeSelectorConfigMap = cm
	nsNodeSelectorConfigMapUpdateTime = time.Now()
	return cm
}

func (sc *SchedulerConfig) getNsNodeSelectorConfigMap(notcache bool) *v1.ConfigMap {
	if sc.nsNodeSelectorConfigMap == nil || notcache == true || time.Since(sc.nsNodeSelectorConfigMapUpdateTime) > sc.configMapTimeOut {
		cm := sc.getSelectorConfigMap(sc.client)
		sc.nsNodeSelectorConfigMap = cm
		sc.nsNodeSelectorConfigMapUpdateTime = time.Now()
	}
	return sc.nsNodeSelectorConfigMap
}

func (sc *SchedulerConfig) NsNodeSelectorGet(request *restful.Request, response *restful.Response) {
	cm := sc.getNsNodeSelectorConfigMap(false)
	if cm == nil {
		response.Write([]byte("undefine NodeSelector configmap"))
		return
	}
	namespaces, _ := GetNamespaces(sc.client)
	nsconfig := GetNsConfigByConfigMap(cm, namespaces)
	tmp := make(map[string]NsNodeSelectorConfigRet)
	for ns, config := range nsconfig {
		nsNodeSelectorConfigRet := NsNodeSelectorConfigRet{}
		if config.Match != nil {
			nsNodeSelectorConfigRet.Match = make([]KeyValue, 0, len(config.Match))
			for key, valueMap := range config.Match {
				if key != "" {
					nsNodeSelectorConfigRet.Match = append(nsNodeSelectorConfigRet.Match, KeyValue{
						Key:   key,
						Value: strings.Join(ListMapString(valueMap), ","),
					})
				}
			}
		}
		if config.MustMatch != nil {
			nsNodeSelectorConfigRet.MustMatch = make([]KeyValue, 0, len(config.MustMatch))
			for key, valueMap := range config.MustMatch {
				if key != "" {
					nsNodeSelectorConfigRet.MustMatch = append(nsNodeSelectorConfigRet.MustMatch, KeyValue{
						Key:   key,
						Value: strings.Join(ListMapString(valueMap), ","),
					})
				}
			}
		}
		if config.NotMatch != nil {
			nsNodeSelectorConfigRet.NotMatch = make([]KeyValue, 0, len(config.NotMatch))
			for key, valueMap := range config.NotMatch {
				if key != "" {
					nsNodeSelectorConfigRet.NotMatch = append(nsNodeSelectorConfigRet.NotMatch, KeyValue{
						Key:   key,
						Value: strings.Join(ListMapString(valueMap), ","),
					})
				}
			}
		}
		if config.MustNotMatch != nil {
			nsNodeSelectorConfigRet.MustNotMatch = make([]KeyValue, 0, len(config.MustNotMatch))
			for key, valueMap := range config.MustNotMatch {
				if key != "" {
					nsNodeSelectorConfigRet.MustNotMatch = append(nsNodeSelectorConfigRet.MustNotMatch, KeyValue{
						Key:   key,
						Value: strings.Join(ListMapString(valueMap), ","),
					})
				}
			}
		}
		tmp[ns] = nsNodeSelectorConfigRet
	}
	response.WriteAsJson(tmp)
}

func (sc *SchedulerConfig) CheckNamespaceSchedulerNodes(request *restful.Request, response *restful.Response) {
	nodes, errNode := GetNodes(sc.client)
	if errNode != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("get nodes error:%v", errNode.Error()),
			Data: ""})
		return
	}
	namespace := request.PathParameter("namespace")
	nsConfig := GetNsConfigByConfigMap(sc.getNsNodeSelectorConfigMap(false), nil)
	selector, err := GetNsLabelSelector(nsConfig, namespace)
	if err != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  err.Error(),
			Data: ""})
		return
	}
	okNodes := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if selector.Matches(labels.Set(node.Labels)) {
			okNodes = append(okNodes, node.Name)
		}
	}
	response.WriteAsJson(ReturnMsg{Code: 0,
		Msg:  "OK",
		Data: fmt.Sprintf("[%s]", strings.Join(okNodes, ","))})
}

func (sc *SchedulerConfig) NsNodeSelectorNodeLabels(request *restful.Request, response *restful.Response) {
	nodes, errNode := GetNodes(sc.client)
	if errNode != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("get nodes error:%v", errNode.Error()),
			Data: ""})
		return
	}
	m := make(LabelValues)
	m[Nsnodeselector_systemlabel] = map[string]struct{}{"*": {}}
	for _, node := range nodes {
		for k, v := range node.Labels {
			//set.Insert(predicates.MakeSelectorByKeyValue(k, v))
			if vm, find := m[k]; find == true {
				vm[v] = struct{}{}
			} else {
				m[k] = map[string]struct{}{v: {}}
			}
		}
	}

	lbs := make([]KeyValue, 0, len(m))
	for k, vm := range m {
		if k != "" {
			lbs = append(lbs, KeyValue{Key: k, Value: strings.Join(ListMapString(vm), ",")})
		}
	}
	buf, _ := json.Marshal(lbs)
	response.WriteAsJson(ReturnMsg{Code: 1,
		Msg:  "OK",
		Data: string(buf)})
}

func (sc *SchedulerConfig) NsNodeSelectorRefresh(request *restful.Request, response *restful.Response) {
	nodes, errNode := GetNodes(sc.client)
	if errNode != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("get nodes error:%v", errNode.Error()),
			Data: ""})
		return
	}
	namespace := request.PathParameter("namespace")
	nsConfig := GetNsConfigByConfigMap(sc.getNsNodeSelectorConfigMap(true), nil)
	selector, err := GetNsLabelSelector(nsConfig, namespace)
	if err != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("GetNsLabelSelector err:%v", err.Error()),
			Data: ""})
		return
	}
	okNodes := make(map[string]struct{})
	for _, node := range nodes {
		if selector.Matches(labels.Set(node.Labels)) {
			okNodes[node.Name] = struct{}{}
		}
	}

	pods, errPods := GetPods(sc.client, namespace)
	if errPods != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("get pods err:%v", errPods.Error()),
			Data: ""})
		return
	}
	errMsg := make([]string, 0, len(pods))
	okMsg := make([]string, 0, len(pods))
	for _, pod := range pods {
		if pod.Spec.NodeName != "" {
			if _, ok := okNodes[pod.Spec.NodeName]; ok == false {
				errDelete := DeletePod(sc.client, pod.Namespace, pod.Name)
				if errDelete != nil {
					errMsg = append(errMsg, fmt.Sprintf("delete pod[%s:%s] err:%v", pod.Namespace, pod.Name, errDelete))
				} else {
					okMsg = append(okMsg, fmt.Sprintf("[%s:%s]:%s", pod.Namespace, pod.Name, pod.Spec.NodeName))
				}
			}
		}
	}
	if len(errMsg) == 0 {
		response.WriteAsJson(ReturnMsg{Code: 0,
			Msg:  "OK",
			Data: strings.Join(okMsg, ",")})
	} else {
		response.WriteAsJson(ReturnMsg{Code: 0,
			Msg:  strings.Join(errMsg, "|"),
			Data: strings.Join(okMsg, ",")})
	}
}

func (sc *SchedulerConfig) NsNodeSelectorAddUpdateOrDelete(request *restful.Request, response *restful.Response) {
	request.Request.ParseForm()
	addUpdateOrDelete := request.PathParameter("addupdateordelete")
	if addUpdateOrDelete != "delete" && addUpdateOrDelete != "add" && addUpdateOrDelete != "update" {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("unknow operation %s", addUpdateOrDelete),
			Data: ""})
		return
	}
	namespace := request.Request.FormValue("namespace")
	matchType := request.Request.FormValue("type")
	matchKey := request.Request.FormValue("key")
	matchValue := request.Request.FormValue("value")

	cm := sc.getNsNodeSelectorConfigMap(true)

	namespaces, _ := GetNamespaces(sc.client)
	nsConfig := GetNsConfigByConfigMap(cm, namespaces)
	if nsConfig == nil {
		nsConfig = make(NsConfig)
	}
	nsConfigItem := nsConfig[namespace]

	notExist := ReturnMsg{Code: 1,
		Msg:  fmt.Sprintf("key [%s] not exist", matchKey),
		Data: ""}
	isExist := ReturnMsg{Code: 1,
		Msg:  fmt.Sprintf("key [%s] is existed", matchKey),
		Data: ""}

	tmpFun := func(lbvs LabelValues) bool {
		if addUpdateOrDelete == "add" {
			if _, exist := lbvs[matchKey]; exist == false {
				lbvs.InsertValue(matchKey, matchValue)
			} else {
				response.WriteAsJson(isExist)
				return false
			}
		} else if addUpdateOrDelete == "delete" {
			if _, exist := lbvs[matchKey]; exist == true {
				delete(lbvs, matchKey)
			} else {
				response.WriteAsJson(notExist)
				return false
			}
		} else {
			if _, exist := lbvs[matchKey]; exist == true {
				delete(lbvs, matchKey)
				lbvs.InsertValue(matchKey, matchValue)
			} else {
				response.WriteAsJson(notExist)
				return false
			}
		}
		return true
	}
	switch matchType {
	case "match":
		if nsConfigItem.Match == nil {
			nsConfigItem.Match = make(LabelValues)
		}
		if tmpFun(nsConfigItem.Match) == false {
			return
		}
	case "mustmatch":
		if nsConfigItem.MustMatch == nil {
			nsConfigItem.MustMatch = make(LabelValues)
		}
		if tmpFun(nsConfigItem.MustMatch) == false {
			return
		}
	case "notmatch":
		if nsConfigItem.NotMatch == nil {
			nsConfigItem.NotMatch = make(LabelValues)
		}
		if tmpFun(nsConfigItem.NotMatch) == false {
			return
		}
	case "mustnotmatch":
		if nsConfigItem.MustNotMatch == nil {
			nsConfigItem.MustNotMatch = make(LabelValues)
		}
		if tmpFun(nsConfigItem.MustNotMatch) == false {
			return
		}
	default:
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  fmt.Sprintf("unknow matchtype %s", matchType),
			Data: ""})
		return
	}
	nsConfig[namespace] = nsConfigItem
	SetNsNodeSelectorConfigMapNsConfig(cm, nsConfig)
	if updateCm, err := CreateOrUpdateSchedulerPolicyConfigMap(sc.client, cm); err != nil {
		response.WriteAsJson(ReturnMsg{Code: 1,
			Msg:  err.Error(),
			Data: ""})
	} else {
		response.WriteAsJson(ReturnMsg{Code: 0,
			Msg:  "OK",
			Data: ""})
		sc.nsNodeSelectorConfigMap = updateCm
	}
}

func StartPolicyHttpServer(client *kubernetes.Clientset, timeout time.Duration, addr, certFile, keyFile, basicAuthFile string) {
	c := &SchedulerConfig{
		client:           client,
		configMapTimeOut: timeout,
	}
	var wsContainer *restful.Container = restful.NewContainer()
	mux := http.NewServeMux()
	handler := http.Handler(mux)

	if basicAuthFile != "" {
		auth, err := newAuthenticatorFromBasicAuthFile(basicAuthFile)
		if err != nil {
			glog.Errorf("Unable to StartPolicyHttpServer: %v", err)
			return
		}
		handler = WithAuthentication(mux, auth)
	}

	wsContainer.Router(restful.CurlyRouter{})
	wsContainer.ServeMux = mux

	ws1 := new(restful.WebService)
	ws1.Path("/nsnodeselector").Consumes("*/*").Produces(restful.MIME_JSON)
	ws1.Route(ws1.GET("/").To(c.NsNodeSelectorGet).
		Doc("show all nsnodeselector").
		Writes(map[string]NsNodeSelectorConfigRet{}))
	ws1.Route(ws1.GET("/check/{namespace}").To(c.CheckNamespaceSchedulerNodes).
		Doc("check which node namespace pod can schedule to").
		Writes(ReturnMsg{}))
	ws1.Route(ws1.GET("/{addupdateordelete}/").To(c.NsNodeSelectorAddUpdateOrDelete).
		Doc("add update, or delete namespace nodeselector match/notmach/mustmatch/mustnotmatch").
		Writes(ReturnMsg{}))
	ws1.Route(ws1.GET("/nodelabels/").To(c.NsNodeSelectorNodeLabels).
		Doc("get all node labels").
		Writes(ReturnMsg{}))
	ws1.Route(ws1.GET("/refresh/{namespace}").To(c.NsNodeSelectorRefresh).
		Doc("get all node labels").
		Writes(ReturnMsg{}))

	wsContainer.Add(ws1)

	serverPolicy := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	go func() {
		if certFile != "" && keyFile != "" { // for https
			glog.Fatal(serverPolicy.ListenAndServeTLS(certFile, keyFile))
		} else { //for http
			glog.Fatal(serverPolicy.ListenAndServe())
		}
	}()
}

// newAuthenticatorFromBasicAuthFile returns an authenticator.Request or an error
func newAuthenticatorFromBasicAuthFile(basicAuthFile string) (authenticator.Request, error) {
	basicAuthenticator, err := passwordfile.NewCSV(basicAuthFile)
	if err != nil {
		return nil, err
	}

	return basicauth.New(basicAuthenticator), nil
}

func WithAuthentication(handler http.Handler, auth authenticator.Request) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, ok, err := auth.AuthenticateRequest(req)
		if ok == false || err != nil {
			if err != nil {
				glog.Errorf("Unable to authenticate the request due to an error: %v", err)
			}
			unauthorizedBasicAuth(w, req)
			return
		}
		handler.ServeHTTP(w, req)
	})
}

func unauthorizedBasicAuth(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("WWW-Authenticate", `Basic realm="kubernetes-master"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
