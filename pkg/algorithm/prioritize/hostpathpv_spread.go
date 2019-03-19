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
		Interface: &HostPathPVSpread{},
	})
}

type HostPathPVSpread struct {
	pvInfo    *algorithm.CachedPersistentVolumeInfo
	pvcInfo   *algorithm.CachedPersistentVolumeClaimInfo
	podInfo   *algorithm.CachedPodInfo
	hasSynced func() bool
}

func (hppvs *HostPathPVSpread) Name() string {
	return "hostpathpvspread"
}

func (hppvs *HostPathPVSpread) Init(clientset *kubernetes.Clientset, informerFactory informers.SharedInformerFactory) error {
	pvInformer := informerFactory.Core().V1().PersistentVolumes()
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	podInformer := informerFactory.Core().V1().Pods()
	hppvs.pvInfo = &algorithm.CachedPersistentVolumeInfo{PersistentVolumeLister: pvInformer.Lister()}
	hppvs.pvcInfo = &algorithm.CachedPersistentVolumeClaimInfo{PersistentVolumeClaimLister: pvcInformer.Lister()}
	hppvs.podInfo = &algorithm.CachedPodInfo{PodLister: podInformer.Lister()}
	pvSynced := pvInformer.Informer().HasSynced
	pvcSynced := pvcInformer.Informer().HasSynced
	podSynced := podInformer.Informer().HasSynced
	hppvs.hasSynced = func() bool {
		return pvSynced() && pvcSynced() && podSynced()
	}

	return nil
}

func (hppvs *HostPathPVSpread) Ready() bool {
	return hppvs.hasSynced()
}

func (hppvs *HostPathPVSpread) mapScoringNode(wg *sync.WaitGroup, pod *v1.Pod, node *v1.Node,
	priority *schedulerapi.HostPriority, errAdd func(error)) {
	defer wg.Done()
	var count int
	for _, podVolume := range pod.Spec.Volumes {
		pv, err := algorithm.GetPodVolumePV(pod, podVolume, hppvs.pvInfo, hppvs.pvcInfo)
		if err != nil {
			errAdd(err)
			continue
		}
		if pv == nil { // is not a hostpath pv
			continue
		}

		if podsMap, err := algorithm.GetHostPathPVUsedPodMap(pv, hppvs.podInfo, node.Name); err != nil {
			errAdd(err)
			continue
		} else {
			if algorithm.IsSharedHostPathPV(pv) {
				if len(podsMap) > 0 {
					count++
				}
			} else {
				count += len(podsMap)
			}
		}
	}
	priority.Host = node.Name
	priority.Score = count
	glog.V(3).Infof("HostPathPVSpread mapScoringNode pod %s:%s to node %s score %d", pod.Namespace, pod.Name, node.Name, count)
}

func (hppvs *HostPathPVSpread) reduceScoringNode(pod *v1.Pod, priorityList schedulerapi.HostPriorityList) error {
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
			fScore = 10 * ((maxCountFloat - float64(priorityList[i].Score)) / maxCountFloat)
		} else {
			fScore = 10
		}
		priorityList[i].Score = int(fScore)
		glog.V(2).Infof("HostPathPVSpread reduceScoringNode pod %s:%s to node %s score:%d", pod.Namespace, pod.Name, priorityList[i].Host, priorityList[i].Score)
	}
	return nil
}

func (hppvs *HostPathPVSpread) NodesScoring(pod *v1.Pod, nodes []v1.Node) (*schedulerapi.HostPriorityList, error) {
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
		go hppvs.mapScoringNode(&wg, pod, node, &priorityList[i], appendError)

	}
	wg.Wait()
	if len(errs) != 0 {
		glog.Errorf("NodesScoring pod %s:%s err:%v", pod.Namespace, pod.Name, errors.NewAggregate(errs))
		return &priorityList, errors.NewAggregate(errs)
	}
	if err := hppvs.reduceScoringNode(pod, priorityList); err != nil {
		glog.Errorf("NodesScoring reduceScoringNode pod %s:%s err:%v", pod.Namespace, pod.Name, err)
		return &priorityList, fmt.Errorf("reduceScoringNode for pod %s:%s err:%v", pod.Namespace, pod.Name, err)
	}
	return &priorityList, nil
}
