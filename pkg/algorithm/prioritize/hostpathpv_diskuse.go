package prioritize

import (
	"fmt"
	"sync"

	"github.com/Rhealb/extender-scheduler/pkg/algorithm"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/api"
)

func init() {
	Regist(&Prioritize{
		Interface: &HostPathPVDiskUse{},
	})
}

type HostPathPVDiskUse struct {
	pvInfo    *algorithm.CachedPersistentVolumeInfo
	pvcInfo   *algorithm.CachedPersistentVolumeClaimInfo
	podInfo   *algorithm.CachedPodInfo
	hasSynced func() bool
}

func (hppvdu *HostPathPVDiskUse) Name() string {
	return "hostpathpvdiskuse"
}

func (hppvdu *HostPathPVDiskUse) Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	pvInformer := informerFactory.Core().V1().PersistentVolumes()
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	podInformer := informerFactory.Core().V1().Pods()
	hppvdu.pvInfo = &algorithm.CachedPersistentVolumeInfo{PersistentVolumeLister: pvInformer.Lister()}
	hppvdu.pvcInfo = &algorithm.CachedPersistentVolumeClaimInfo{PersistentVolumeClaimLister: pvcInformer.Lister()}
	hppvdu.podInfo = &algorithm.CachedPodInfo{PodLister: podInformer.Lister()}
	pvSynced := pvInformer.Informer().HasSynced
	pvcSynced := pvcInformer.Informer().HasSynced
	podSynced := podInformer.Informer().HasSynced
	hppvdu.hasSynced = func() bool {
		return pvSynced() && pvcSynced() && podSynced()
	}

	return nil
}

func (hppvdu *HostPathPVDiskUse) Ready() bool {
	return hppvdu.hasSynced()
}

func (hppvdu *HostPathPVDiskUse) mapScoringNode(wg *sync.WaitGroup, pod *v1.Pod, node *v1.Node,
	priority *schedulerapi.HostPriority, errAdd func(error)) {
	defer wg.Done()
	var allocable int64
	var quota int64
	if nodeDiskInfo, err := algorithm.GetNodeDiskInfo(node); err != nil {
		errAdd(err)
	} else if nodeMountInfo, err2 := algorithm.GetNodeHostPathPVMountInfo(node.Name, hppvdu.pvInfo, hppvdu.podInfo); err2 != nil {
		errAdd(err2)
	} else {
		for _, info := range nodeDiskInfo {
			allocable += info.Allocable
		}
		for _, mi := range nodeMountInfo.MountInfos {
			quota += mi.VolumeQuotaSize
		}
	}
	var count int
	if allocable <= 0 || allocable <= quota {
		count = 0
	} else {
		count = int((float64(100) * float64(allocable-quota)) / float64(allocable))
	}
	priority.Host = node.Name
	priority.Score = count
	glog.V(3).Infof("HostPathPVDiskUse mapScoringNode pod %s:%s to node %s score %d", pod.Namespace, pod.Name, node.Name, count)
}

func (hppvdu *HostPathPVDiskUse) reduceScoringNode(pod *v1.Pod, priorityList schedulerapi.HostPriorityList) error {
	var maxCount int
	for i := range priorityList {
		if priorityList[i].Score > maxCount {
			maxCount = priorityList[i].Score
		}
	}
	maxCountFloat := float64(maxCount)

	var fScore float64
	for i := range priorityList {
		if maxCount > 0 {
			fScore = 10 * (float64(priorityList[i].Score) / maxCountFloat)
		} else {
			fScore = 0
		}
		priorityList[i].Score = int(fScore)
		glog.V(2).Infof("HostPathPVDiskUse reduceScoringNode pod %s:%s to node %s score:%d", pod.Namespace, pod.Name, priorityList[i].Host, priorityList[i].Score)
	}
	return nil
}

func (hppvdu *HostPathPVDiskUse) NodesScoring(pod *v1.Pod, nodes []v1.Node) (*schedulerapi.HostPriorityList, error) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs []error
	)
	priorityList := make(schedulerapi.HostPriorityList, len(nodes))
	appendError := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}
	for i := range nodes {
		wg.Add(1)
		node := &nodes[i]
		go hppvdu.mapScoringNode(&wg, pod, node, &priorityList[i], appendError)

	}
	wg.Wait()
	if len(errs) != 0 {
		glog.Errorf("NodesScoring pod %s:%s err:%v", pod.Namespace, pod.Name, errors.NewAggregate(errs))
		return &priorityList, errors.NewAggregate(errs)
	}
	if err := hppvdu.reduceScoringNode(pod, priorityList); err != nil {
		glog.Errorf("NodesScoring reduceScoringNode pod %s:%s err:%v", pod.Namespace, pod.Name, err)
		return &priorityList, fmt.Errorf("reduceScoringNode for pod %s:%s err:%v", pod.Namespace, pod.Name, err)
	}
	return &priorityList, nil
}
