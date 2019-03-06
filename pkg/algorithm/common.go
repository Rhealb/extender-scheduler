package algorithm

import (
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"
)

// PersistentVolumeInfo interface represents anything that can get persistent volume object by PV ID.
type PersistentVolumeInfo interface {
	GetPersistentVolumeInfo(pvID string) (*v1.PersistentVolume, error)
	List() (ret []*v1.PersistentVolume, err error)
}

type CachedPersistentVolumeInfo struct {
	corelisters.PersistentVolumeLister
}

func (c *CachedPersistentVolumeInfo) GetPersistentVolumeInfo(pvID string) (*v1.PersistentVolume, error) {
	return c.Get(pvID)
}

func (c *CachedPersistentVolumeInfo) List() (ret []*v1.PersistentVolume, err error) {
	return c.PersistentVolumeLister.List(labels.Everything())
}

// PersistentVolumeClaimInfo interface represents anything that can get a PVC object in
// specified namespace with specified name.
type PersistentVolumeClaimInfo interface {
	GetPersistentVolumeClaimInfo(namespace string, name string) (*v1.PersistentVolumeClaim, error)
}

// CachedPersistentVolumeClaimInfo implements PersistentVolumeClaimInfo
type CachedPersistentVolumeClaimInfo struct {
	corelisters.PersistentVolumeClaimLister
}

// GetPersistentVolumeClaimInfo fetches the claim in specified namespace with specified name
func (c *CachedPersistentVolumeClaimInfo) GetPersistentVolumeClaimInfo(namespace string, name string) (*v1.PersistentVolumeClaim, error) {
	return c.PersistentVolumeClaims(namespace).Get(name)
}

type PodInfo interface {
	List(all bool) (ret []*v1.Pod, err error)
	Get(namespace, name string) (*v1.Pod, error)
	FilterByNode(nodeName string, all bool) (ret []*v1.Pod, err error)
	FilterByNodeAndPVC(nodeName, pvcNamespace, pvcName string, all bool) (ret []*v1.Pod, err error)
}

type CachedPodInfo struct {
	corelisters.PodLister
}

func isPodReady(p *v1.Pod) bool {
	return v1.PodSucceeded != p.Status.Phase &&
		v1.PodFailed != p.Status.Phase &&
		p.DeletionTimestamp == nil
}

func isPodUsePVC(p *v1.Pod, pvcNamespace, pvcName string) bool {
	if p.Namespace != pvcNamespace {
		return false
	}
	for _, podVolume := range p.Spec.Volumes {
		if pvcSource := podVolume.VolumeSource.PersistentVolumeClaim; pvcSource != nil {
			if pvcSource.ClaimName == pvcName {
				return true
			}
		}
	}
	return false
}

func (c *CachedPodInfo) Get(namespace, name string) (*v1.Pod, error) {
	return c.Pods(namespace).Get(name)
}

func (c *CachedPodInfo) FilterByNodeAndPVC(nodeName, pvcNamespace, pvcName string, all bool) (ret []*v1.Pod, err error) {
	filter := func(p *v1.Pod) bool {
		//		fmt.Printf("patrick debug filter %s %s %t %t\n", p.Name, p.Spec.NodeName, isPodUsePVC(p, pvcNamespace, pvcName), isPodReady(p))
		if (nodeName == "" || p.Spec.NodeName == nodeName) && isPodUsePVC(p, pvcNamespace, pvcName) {
			if all {
				return true
			} else {
				return isPodReady(p)
			}
		} else {
			return false
		}
	}
	return c.list(filter)
}

func (c *CachedPodInfo) FilterByNode(nodeName string, all bool) (ret []*v1.Pod, err error) {
	filter := func(p *v1.Pod) bool {
		if p.Spec.NodeName == nodeName {
			if all {
				return true
			} else {
				return isPodReady(p)
			}
		} else {
			return false
		}
	}
	return c.list(filter)
}

func (c *CachedPodInfo) List(all bool) (ret []*v1.Pod, err error) {
	if all {
		return c.list(nil)
	} else {
		return c.list(isPodReady)
	}
}

func (c *CachedPodInfo) list(filter func(pod *v1.Pod) bool) (ret []*v1.Pod, err error) {
	ret, err = c.PodLister.List(labels.Everything())
	if err != nil {
		return ret, err
	} else {
		filted := make([]*v1.Pod, 0, len(ret))
		for _, pod := range ret {
			if filter == nil || filter(pod) {
				filted = append(filted, pod)
			}
		}
		return filted, nil
	}
}
