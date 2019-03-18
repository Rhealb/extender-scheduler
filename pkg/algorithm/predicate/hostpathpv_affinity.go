package predicate

import (
	"fmt"

	"github.com/Rhealb/extender-scheduler/pkg/algorithm"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

func init() {
	Regist(&Predicate{
		Interface: &HostPathPVAffinity{},
	})
}

type HostPathPVAffinity struct {
	pvInfo    *algorithm.CachedPersistentVolumeInfo
	pvcInfo   *algorithm.CachedPersistentVolumeClaimInfo
	podInfo   *algorithm.CachedPodInfo
	clientset *kubernetes.Clientset
	hasSynced func() bool
}

func (hppva *HostPathPVAffinity) Name() string {
	return "hostpathpvaffinity"
}

func (hppva *HostPathPVAffinity) Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	pvInformer := informerFactory.Core().V1().PersistentVolumes()
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	podInformer := informerFactory.Core().V1().Pods()
	hppva.pvInfo = &algorithm.CachedPersistentVolumeInfo{PersistentVolumeLister: pvInformer.Lister()}
	hppva.pvcInfo = &algorithm.CachedPersistentVolumeClaimInfo{PersistentVolumeClaimLister: pvcInformer.Lister()}
	hppva.podInfo = &algorithm.CachedPodInfo{PodLister: podInformer.Lister()}
	hppva.clientset = clientset
	pvSynced := pvInformer.Informer().HasSynced
	pvcSynced := pvcInformer.Informer().HasSynced
	podSynced := podInformer.Informer().HasSynced
	hppva.hasSynced = func() bool {
		return pvSynced() && pvcSynced() && podSynced()
	}

	return nil
}

func (hppva *HostPathPVAffinity) Ready() bool {
	return hppva.hasSynced()
}

func (hppva *HostPathPVAffinity) podPVMatchNode(pod *v1.Pod, node *v1.Node, podVolume v1.Volume) (bool, error) {
	pv, err := algorithm.GetPodVolumePV(pod, podVolume, hppva.pvInfo, hppva.pvcInfo)
	if err != nil {
		return false, newPredicateError(hppva.Name(), fmt.Sprintf("node:%s, GetPodVolumePV err:%v", node.Name, err.Error))
	}
	if pv == nil { // pv is not a hostpathpv
		return true, nil
	}
	mountInfos, err := algorithm.GetHostPathPVMountInfoList(pv)
	isShare := algorithm.IsSharedHostPathPV(pv)
	isKeep := algorithm.IsKeepHostPathPV(pv)
	switch {
	case isShare && isKeep: // keep false
		if len(mountInfos) == 0 { // pv has no mount info
			nodesMap, err := algorithm.GetHostPathPVUsedNodeMap(hppva.clientset, pv, hppva.podInfo)
			if err != nil {
				return false, newPredicateError(hppva.Name(), fmt.Sprintf("node:%s, GetHostPathPVUsedNodeMap err:%v", node.Name, err.Error))
			}
			if len(nodesMap) == 0 {
				glog.Infof("keep false PodMatchNode for %s:%s pv %s to node:%s no mountInfos and nodesMap is empty", pod.Namespace, pod.Name, pv.Name, node.Name)
				return true, nil
			} else {
				if nodesMap[node.Name] == true {
					glog.Infof("keep false PodMatchNode for %s:%s pv %s to node:%s no mountInfos and nodesMap has node", pod.Namespace, pod.Name, pv.Name, node.Name)
					return true, nil
				} else {
					glog.Infof("keep false PodMatchNode for %s:%s pv %s to node:%s no mountInfos and nodesMap %v not include node", pod.Namespace, pod.Name, pv.Name, node.Name, nodesMap)
					return false, nil
				}
			}
		}
		nodeMap := make(map[string]struct{})
		for _, info := range mountInfos {
			if info.NodeName == node.Name {
				glog.Infof("keep false PodMatchNode for %s:%s pv %s to node:%s has mountInfos and node is included", pod.Namespace, pod.Name, pv.Name, node.Name)
				return true, nil
			}
			nodeMap[info.NodeName] = struct{}{}
		}
		glog.Infof("keep false PodMatchNode for %s:%s pv %s to node:%s has mountInfos %v and node is node included", pod.Namespace, pod.Name, pv.Name, node.Name, nodeMap)
		return false, nil
	case isShare && !isKeep: // none false
		glog.Infof("none false PodMatchNode for %s:%s pv %s to node:%s always return true", pod.Namespace, pod.Name, pv.Name, node.Name)
		return true, nil
	case !isShare && isKeep: // keep true
		if len(mountInfos) == 0 { // create new
			glog.Infof("keep true PodMatchNode for %s:%s pv %s to node:%s no mountInfos always create new", pod.Namespace, pod.Name, pv.Name, node.Name)
			return true, nil
		}
		hasEmpytItem := false
		emptyNodeMap := make(map[string]struct{})
		for _, info := range mountInfos {
			if ok, err := algorithm.IsHostPathPVHasEmptyItemForNode(pv, info.NodeName, hppva.podInfo); err != nil {
				return false, newPredicateError(hppva.Name(), fmt.Sprintf("node:%s, IsHostPathPVHasEmptyItemForNode err:%v", node.Name, err.Error))
			} else if ok == true {
				if node.Name == info.NodeName {
					glog.Infof("keep true PodMatchNode for %s:%s pv %s to node:%s mountInfos include node and has empty item", pod.Namespace, pod.Name, pv.Name, node.Name)
					return true, nil
				} else {
					hasEmpytItem = true
					emptyNodeMap[info.NodeName] = struct{}{}
				}
			}
		}
		if hasEmpytItem { // one node has no used dir and the node is not we check node
			glog.Infof("keep true PodMatchNode for %s:%s pv %s to node:%s mountInfos and nodes %v has empty dir", pod.Namespace, pod.Name, pv.Name, node.Name, emptyNodeMap)
			return false, nil
		} else { // create new dir
			glog.Infof("keep true PodMatchNode for %s:%s pv %s to node:%s mountInfos has no empty dir create new", pod.Namespace, pod.Name, pv.Name, node.Name)
			return true, nil
		}
	case !isShare && !isKeep: // none true
		glog.Infof("none true PodMatchNode for %s:%s pv %s to node:%s always create new dir", pod.Namespace, pod.Name, pv.Name, node.Name)
		return true, nil // always create new dir
	}
	//  It's impossible to get here.
	glog.Errorf("check pod:%s:%s pv %s to node %s,It's impossible to get here", pod.Namespace, pod.Name, pv.Name, node.Name)

	return true, nil
}

func (hppva *HostPathPVAffinity) PodMatchNode(pod *v1.Pod, node *v1.Node) (bool, error) {
	for _, podVolume := range pod.Spec.Volumes {
		if ok, err := hppva.podPVMatchNode(pod, node, podVolume); err != nil {
			return false, err
		} else if ok == false {
			return false, nil
		}
	}
	return true, nil
}
