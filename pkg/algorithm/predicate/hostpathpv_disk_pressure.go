package predicate

import (
	"fmt"
	"k8s-plugins/extender-scheduler/pkg/algorithm"
	"sort"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

func init() {
	Regist(&Predicate{
		Interface: &HostPathPVDiskPressure{},
	})
}

type DiskInfo struct {
	path     string
	size     int64
	disabled bool
}
type DiskInfoList []DiskInfo

func (l DiskInfoList) Len() int { return len(l) }
func (l DiskInfoList) Less(i, j int) bool {
	if l[i].size != l[j].size {
		return l[i].size < l[j].size
	} else {
		return l[i].path < l[j].path
	}
}
func (l DiskInfoList) Swap(i, j int) { l[i], l[j] = l[j], l[i] }

type HostPathPVDiskPressure struct {
	pvInfo    *algorithm.CachedPersistentVolumeInfo
	pvcInfo   *algorithm.CachedPersistentVolumeClaimInfo
	podInfo   *algorithm.CachedPodInfo
	hasSynced func() bool
}

func (hppvdp *HostPathPVDiskPressure) Name() string {
	return "hostpathpvdiskpressure"
}

func (hppvdp *HostPathPVDiskPressure) Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	pvInformer := informerFactory.Core().V1().PersistentVolumes()
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	podInformer := informerFactory.Core().V1().Pods()
	hppvdp.pvInfo = &algorithm.CachedPersistentVolumeInfo{PersistentVolumeLister: pvInformer.Lister()}
	hppvdp.pvcInfo = &algorithm.CachedPersistentVolumeClaimInfo{PersistentVolumeClaimLister: pvcInformer.Lister()}
	hppvdp.podInfo = &algorithm.CachedPodInfo{PodLister: podInformer.Lister()}
	pvSynced := pvInformer.Informer().HasSynced
	pvcSynced := pvcInformer.Informer().HasSynced
	podSynced := podInformer.Informer().HasSynced
	hppvdp.hasSynced = func() bool {
		return pvSynced() && pvcSynced() && podSynced()
	}

	return nil
}

func (hppvdp *HostPathPVDiskPressure) Ready() bool {
	return hppvdp.hasSynced()
}

func (hppvdp *HostPathPVDiskPressure) getPodHostpathOfNodeDiskInfos(pod *v1.Pod, nodename string) (totalSize int64, infos DiskInfoList, hasHostpathPV bool, err error) {
	list := make(DiskInfoList, 0)
	for i, podVolume := range pod.Spec.Volumes {
		pv, err := algorithm.GetPodVolumePV(pod, podVolume, hppvdp.pvInfo, hppvdp.pvcInfo)
		if err != nil {
			return 0, nil, hasHostpathPV, fmt.Errorf("get pv of pod %s:%s, volume:%d err:%v", pod.Namespace, pod.Name, i, err)
		}
		if pv == nil { // pv is not a hostpathpv
			continue
		}
		if algorithm.IsCommonHostPathPV(pv) == false {
			continue
		}
		hasHostpathPV = true
		capacity, _ := algorithm.GetHostPathPVCapacity(pv)

		if ok, err := algorithm.IsHostPathPVHasEmptyItemForNode(pv, nodename, hppvdp.podInfo); err != nil {
			return 0, nil, hasHostpathPV, err
		} else if ok {
			continue
		} else {
			list = append(list, DiskInfo{size: capacity})
			totalSize += capacity
		}
	}
	sort.Sort(list)
	return totalSize, list, hasHostpathPV, nil
}

func (hppvdp *HostPathPVDiskPressure) getNodeDiskInfos(node *v1.Node) (allocableSize int64, infos DiskInfoList, err error) {
	if diskInfos, err := algorithm.GetNodeDiskInfo(node); err != nil {
		return 0, DiskInfoList{}, err
	} else if len(diskInfos) == 0 {
		return 0, DiskInfoList{}, err
	} else {
		ret := make(DiskInfoList, 0, len(diskInfos))
		for _, info := range diskInfos {
			ret = append(ret, DiskInfo{
				path:     info.MountPath,
				size:     info.Allocable,
				disabled: info.Disabled,
			})
		}
		nodeMountInfo, err := algorithm.GetNodeHostPathPVMountInfo(node.Name, hppvdp.pvInfo, hppvdp.podInfo)
		if err != nil {
			return 0, DiskInfoList{}, err
		}
		for _, mi := range nodeMountInfo.MountInfos {
			for i := range ret {
				if strings.HasPrefix(mi.HostPath, ret[i].path) {
					ret[i].size -= mi.VolumeQuotaSize
					break
				} else if mi.HostPath == "" { // this mount info is not update to pv's annotation, this pv dir maybe create at any node quota disk
					ret[i].size -= mi.VolumeQuotaSize
				}
			}
		}
		for i := range ret {
			if ret[i].size < 0 {
				ret[i].size = 0
			}
			allocableSize += ret[i].size
		}
		sort.Sort(ret)
		return allocableSize, ret, nil
	}
}

func canRequestMatch(request, haveNow DiskInfoList) bool {
	var ir, ih int
	for {
		if ir >= len(request) || ih >= len(haveNow) {
			break
		}
		if request[ir].size <= haveNow[ih].size {
			haveNow[ih].size -= request[ir].size
			ir += 1
			continue
		} else {
			ih += 1
			continue
		}
	}
	if ir >= len(request) && ih >= len(haveNow) {
		return true
	} else if ir >= len(request) {
		return true
	}
	return false
}

func (hppvdp *HostPathPVDiskPressure) PodMatchNode(pod *v1.Pod, node *v1.Node) (bool, error) {
	podRequestSize, podRequestList, hasHostpathPV, errPod := hppvdp.getPodHostpathOfNodeDiskInfos(pod, node.Name)
	if errPod != nil {
		return false, newPredicateError(hppvdp.Name(), fmt.Sprintf("node:%s, getPodHostpathOfNodeDiskInfos err:%v", node.Name, errPod))
	}
	if hasHostpathPV == false {
		return true, nil
		glog.V(4).Infof("pod %s:%s has no hostpathpv", pod.Namespace, pod.Name)
	}
	if podRequestSize == 0 || len(podRequestList) == 0 {
		glog.Infof("pod %s:%s for node %s run directly %d, %v", pod.Namespace, pod.Name, node.Name, podRequestSize, podRequestList)
		return true, nil
	}
	nodeAllocableSize, diskInfo, errNode := hppvdp.getNodeDiskInfos(node)
	if errNode != nil {
		return false, newPredicateError(hppvdp.Name(), fmt.Sprintf("node:%s getNodeDiskInfos:%v", node.Name, errNode))
	}
	if podRequestSize > nodeAllocableSize {
		if nodeAllocableSize <= 0 {
			return false, nil
		}
		return false, newPredicateError(hppvdp.Name(), fmt.Sprintf("node:%s, podRequst:%d, nodeAllocableSize:%d", node.Name, podRequestSize, nodeAllocableSize))
	}
	if canRequestMatch(podRequestList, diskInfo) == false {
		return false, newPredicateError(hppvdp.Name(), fmt.Sprintf("node:%s, notMatch podRequst:%v, nodeAllocableSize:%v", node.Name, podRequestList, diskInfo))
	}
	glog.Infof("pod %s for node %s match %d, %v, %d, %v", pod.Name, node.Name, podRequestSize, podRequestList, nodeAllocableSize, diskInfo)
	return true, nil
}
