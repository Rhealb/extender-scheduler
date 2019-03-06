package predicate

import (
	"encoding/json"
	"fmt"
	"time"

	"strings"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

const (
	nsnodeselector_configmap_ns   = "kube-system"
	nsnodeselector_configmap_name = "nsnodeselector"
	Nsnodeselector_systemlabel    = "enndata.cn/systemnode"
	nsnodeselector_itemname       = "nsnodeselector.json"
)

var (
	nsNodeSelectorConfigMap             *v1.ConfigMap = nil
	nsNodeSelectorConfigMapUpdateTime   time.Time
	nsNodeSelectorConfigMapCacheTimeOut time.Duration = 5 * time.Second
)

func init() {
	Regist(&Predicate{
		Interface: &NamespacesNodeSelector{},
	})
}

type NamespacesNodeSelector struct {
	clientset *kubernetes.Clientset
}

func (nsns *NamespacesNodeSelector) Name() string {
	return "namespacenodeselector"
}

func (nsns *NamespacesNodeSelector) getConfigCM() *v1.ConfigMap {
	if nsNodeSelectorConfigMap != nil && time.Since(nsNodeSelectorConfigMapUpdateTime) <= nsNodeSelectorConfigMapCacheTimeOut {
		glog.V(4).Infof("used cached config map")
		return nsNodeSelectorConfigMap
	}
	cm, err := nsns.clientset.CoreV1().ConfigMaps(nsnodeselector_configmap_ns).Get(nsnodeselector_configmap_name, meta_v1.GetOptions{})
	if err != nil || cm == nil {
		glog.Errorf("get config map %s:%s err:%v", nsnodeselector_configmap_ns, nsnodeselector_configmap_name, err)
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
	glog.V(4).Infof("cache and update configmap %s:%s", nsnodeselector_configmap_ns, nsnodeselector_configmap_name)
	return cm
}

func (nsns *NamespacesNodeSelector) Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	nsns.clientset = clientset
	return nil
}

func (nsns *NamespacesNodeSelector) Ready() bool {
	return true
}

func (nsns *NamespacesNodeSelector) PodMatchNode(pod *v1.Pod, node *v1.Node) (bool, error) {
	config := GetNsConfigByConfigMap(nsns.getConfigCM(), nil)

	selector, errGet := GetNsLabelSelector(config, pod.Namespace)
	if errGet != nil {
		return false, newPredicateError(nsns.Name(), fmt.Sprintf("node:%s, GetNsLabelSelector err:%v", node.Name, errGet.Error()))
	}

	if !selector.Matches(labels.Set(node.Labels)) {
		glog.V(4).Infof("NamespacesNodeSelector pod %s:%s namespace selector %v not match node:%s", pod.Namespace, pod.Name, selector, node.Name)
		return false, nil
	}
	glog.Infof("NamespacesNodeSelector pod %s:%s namespace selector[%v]  match node:%s", pod.Namespace, pod.Name, selector, node.Name)
	return true, nil
}

func GetNsLabelSelector(nsConfig NsConfig, ns string) (labels.Selector, error) {
	if nsConfig == nil {
		ret, err := labels.Parse(fmt.Sprintf("!%s", Nsnodeselector_systemlabel))
		if err != nil {
			return labels.Everything(), nil
		}
		return ret, nil
	}
	if nsConfigItem, exist := nsConfig[ns]; exist == false {
		ret, err := labels.Parse(fmt.Sprintf("!%s", Nsnodeselector_systemlabel))
		if err != nil {
			return labels.Everything(), nil
		}
		return ret, nil
	} else {
		var countItem int
		if nsConfigItem.Match == nil {
			nsConfigItem.Match = make(LabelValues)
		}
		if nsConfigItem.MustMatch == nil {
			nsConfigItem.MustMatch = make(LabelValues)
		}
		if nsConfigItem.NotMatch == nil {
			nsConfigItem.NotMatch = make(LabelValues)
		}
		if nsConfigItem.MustNotMatch == nil {
			nsConfigItem.MustNotMatch = make(LabelValues)
		}
		countItem = len(nsConfigItem.Match) + len(nsConfigItem.MustMatch) + len(nsConfigItem.NotMatch) + len(nsConfigItem.MustNotMatch)
		strTmps := make([]string, 0, countItem)

		for key, mapValue := range nsConfigItem.Match {
			if key != "" {
				if MapHasString(mapValue, "", "*") == false {
					strTmps = append(strTmps, fmt.Sprintf("%s in (%s)", key, strings.Join(ListMapString(mapValue), ",")))
				} else {
					strTmps = append(strTmps, fmt.Sprintf("%s", key))
				}
			}
		}
		for key, mapValue := range nsConfigItem.MustMatch {
			if key != "" {
				if MapHasString(mapValue, "", "*") == false {
					strTmps = append(strTmps, fmt.Sprintf("%s in (%s)", key, strings.Join(ListMapString(mapValue), ",")))
				} else {
					strTmps = append(strTmps, fmt.Sprintf("%s", key))
				}
			}
		}

		for key, mapValue := range nsConfigItem.NotMatch {
			if key != "" {
				if MapHasString(mapValue, "", "*") == false {
					strTmps = append(strTmps, fmt.Sprintf("%s notin (%s)", key, strings.Join(ListMapString(mapValue), ",")))
				} else {
					strTmps = append(strTmps, fmt.Sprintf("!%s", key))
				}
			}
		}
		for key, mapValue := range nsConfigItem.MustNotMatch {
			if key != "" {
				if MapHasString(mapValue, "", "*") == false {
					strTmps = append(strTmps, fmt.Sprintf("%s notin (%s)", key, strings.Join(ListMapString(mapValue), ",")))
				} else {
					strTmps = append(strTmps, fmt.Sprintf("!%s", key))
				}
			}
		}
		return labels.Parse(strings.Join(strTmps, ","))

	}
	return labels.Everything(), nil
}

func MapHasString(m map[string]struct{}, strs ...string) bool {
	for _, str := range strs {
		if _, ok := m[str]; ok == true {
			return true
		}
	}
	return false
}

func ListMapString(m map[string]struct{}) []string {
	ret := make([]string, 0, len(m))
	for str := range m {
		ret = append(ret, str)
	}
	return ret
}

func GetNsConfigByConfigMap(cm *v1.ConfigMap, namespaces []*v1.Namespace) NsConfig {
	ret := make(map[string]NsConfigItem)
	var data string = "{}"

	if cm != nil && cm.Data[nsnodeselector_itemname] != "" {
		data = cm.Data[nsnodeselector_itemname]
	}

	err := json.Unmarshal([]byte(data), &ret)
	if err != nil {
		glog.Errorf("getNsConfigByConfigMap Unmarshal error:%v\n", err)
		return nil
	}
	if namespaces != nil {
		for _, ns := range namespaces {
			if _, find := ret[ns.Name]; find == false {
				ret[ns.Name] = NsConfigItem{}
			}
		}
	}
	for ns, item := range ret {
		if item.NotMatch == nil { // default namespace can't schedule to systemlabel node
			item.NotMatch = make(LabelValues)
			item.NotMatch[Nsnodeselector_systemlabel] = map[string]struct{}{"*": struct{}{}}
		}
		ret[ns] = item
	}
	return ret
}

type LabelValues map[string]map[string]struct{}

func (lvs LabelValues) InsertValue(k, v string) {
	var strs []string
	if v != "" {
		strs = strings.Split(v, ",")
	} else {
		strs = []string{""}
	}
	if valueMap, exist := lvs[k]; exist == true {
		for _, str := range strs {
			valueMap[str] = struct{}{}
		}
	} else {
		valueMap = make(map[string]struct{})
		for _, str := range strs {
			valueMap[str] = struct{}{}
		}
		lvs[k] = valueMap
	}
}

type NsConfigItem struct {
	Match        LabelValues // it's used by new created pod, created pod if not match will not be deleted
	MustMatch    LabelValues // it's used by new created pod, created pod if not match will deleted by controller
	NotMatch     LabelValues // it's used by new created pod, created pod if match will not be deleted
	MustNotMatch LabelValues // it's used by new created pod, created pod if match will deleted by controller
}

type NsConfig map[string]NsConfigItem
