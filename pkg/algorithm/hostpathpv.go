package algorithm

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	hostpath "k8s-plugins/csi-plugin/hostpathpv/pkg/hostpath"
	"k8s-plugins/csi-plugin/hostpathpv/pkg/hostpath/xfsquotamanager"
	"k8s-plugins/csi-plugin/hostpathpv/pkg/hostpath/xfsquotamanager/common"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	//	corelisters "k8s.io/client-go/listers/core/v1"
)

func IsHostPathPV(pv *v1.PersistentVolume) bool {
	if pv != nil && pv.Spec.HostPath != nil {
		return true
	}
	return false
}

func IsCSIHostPathPV(pv *v1.PersistentVolume) bool {
	if pv != nil && pv.Spec.CSI != nil && strings.Contains(strings.ToLower(pv.Spec.CSI.Driver), "hostpath") == true {
		return true
	}
	return false
}

func IsCommonHostPathPV(pv *v1.PersistentVolume) bool {
	return IsHostPathPV(pv) || IsCSIHostPathPV(pv)
}

func hasAnnotation(annotations map[string]string, key, value string) bool {
	if annotations != nil {
		if v, exist := annotations[key]; exist && v == value {
			return true
		}
	}
	return false
}

func IsSharedHostPathPV(pv *v1.PersistentVolume) bool {
	// default not shared
	if IsCommonHostPathPV(pv) && hasAnnotation(pv.Annotations, common.PVHostPathQuotaForOnePod, "false") {
		return true
	}
	return false
}

func IsKeepHostPathPV(pv *v1.PersistentVolume) bool {
	// default keep
	if IsCommonHostPathPV(pv) {
		if hasAnnotation(pv.Annotations, common.PVHostPathMountPolicyAnn, common.PVHostPathNone) {
			return false
		}
		return true
	}
	return false
}

func GetHostPathPVUsedNodeMap(pv *v1.PersistentVolume, podInfo PodInfo) (map[string]bool, error) {
	ret := make(map[string]bool)
	if pv.Spec.ClaimRef == nil {
		return ret, fmt.Errorf("pv %s has not bound", pv.Name)
	}
	pods, err := podInfo.FilterByNodeAndPVC("", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name, false)
	if err != nil {
		return ret, fmt.Errorf("filter pods used pvc %s:%s err:%v", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name, err)
	}
	for _, pod := range pods {
		if pod.Spec.NodeName != "" {
			ret[pod.Spec.NodeName] = true
		}
	}
	return ret, nil
}

func GetHostPathPVUsedPodMap(pv *v1.PersistentVolume, podInfo PodInfo, nodeName string) (map[string]bool, error) {
	ret := make(map[string]bool)
	if pv.Spec.ClaimRef == nil {
		return ret, fmt.Errorf("pv %s has not bound", pv.Name)
	}
	pods, err := podInfo.FilterByNodeAndPVC(nodeName, pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name, false)
	if err != nil {
		return ret, fmt.Errorf("filter pods used pvc %s:%s err:%v", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name, err)
	}
	for _, pod := range pods {
		ret[fmt.Sprintf("%s:%s", pod.Namespace, pod.Name)] = true
	}
	return ret, nil
}

func IsHostPathPVHasEmptyItemForNode(pv *v1.PersistentVolume, nodeName string, podInfo PodInfo) (bool, error) {
	if IsCommonHostPathPV(pv) {
		if IsSharedHostPathPV(pv) {
			return IsHostPathPVMountOnNode(pv, nodeName), nil
		} else {
			if IsKeepHostPathPV(pv) == false {
				return false, nil
			} else {
				mountInfos, err := GetHostPathPVMountInfoList(pv)
				if err != nil {
					return false, err
				}
				if len(mountInfos) == 0 {
					return false, nil
				}
				if pv.Spec.ClaimRef == nil {
					return false, fmt.Errorf("pv %s has not bound", pv.Name)
				}
				for _, info := range mountInfos {
					if info.NodeName == nodeName {
						ret, err := podInfo.FilterByNodeAndPVC(nodeName, pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name, false)
						if err != nil {
							return false, fmt.Errorf("FilterByNodeAndPVC err:%v", err)
						}
						return len(info.MountInfos) > 0 && len(ret) < len(info.MountInfos), nil
					}
				}
				// has no mount info on nodeName node
				return false, nil
			}
		}
	} else {
		return false, fmt.Errorf("pv %s is not a hostpath pv", pv.Name)
	}
}

func GetPodVolumePV(pod *v1.Pod, volume v1.Volume, pvInfo PersistentVolumeInfo, pvcInfo PersistentVolumeClaimInfo) (pv *v1.PersistentVolume, err error) {
	if pvcSource := volume.VolumeSource.PersistentVolumeClaim; pvcSource != nil {
		pvc, errPvc := pvcInfo.GetPersistentVolumeClaimInfo(pod.Namespace, pvcSource.ClaimName)
		if errPvc != nil || pvc == nil {
			return nil, fmt.Errorf("get pvc %s error:%v", pvcSource.ClaimName, errPvc)
		}
		if pvc.Status.Phase != v1.ClaimBound || pvc.Spec.VolumeName == "" {
			return nil, fmt.Errorf("pvc %s is not bound", pvcSource.ClaimName)
		}
		pv, errPv := pvInfo.GetPersistentVolumeInfo(pvc.Spec.VolumeName)
		if errPv != nil || pv == nil {
			return nil, fmt.Errorf("failed to fetch PV %q err=%v", pvc.Spec.VolumeName, err)
		}
		return pv, nil
	}
	return nil, nil
}

func GetNodeDiskInfo(node *v1.Node) (xfsquotamanager.NodeDiskQuotaInfoList, error) {
	if node.Annotations != nil && node.Annotations[common.NodeDiskQuotaInfoAnn] != "" {
		nodeDiskQuotaInfoList := xfsquotamanager.NodeDiskQuotaInfoList{}
		err := json.Unmarshal([]byte(node.Annotations[common.NodeDiskQuotaInfoAnn]), &nodeDiskQuotaInfoList)
		if err != nil {
			return xfsquotamanager.NodeDiskQuotaInfoList{}, fmt.Errorf("getNodeDiskInfo Unmarshal NodeDiskQuotaInfoAnn err:%v", err)
		}
		return nodeDiskQuotaInfoList, nil
	}
	return xfsquotamanager.NodeDiskQuotaInfoList{}, nil
}

func GetNodeHostPathPVMountInfo(nodeName string, pvInfo PersistentVolumeInfo, podInfo PodInfo) (hostpath.HostPathPVMountInfo, error) {
	ret := hostpath.HostPathPVMountInfo{
		MountInfos: hostpath.MountInfoList{},
		NodeName:   nodeName,
	}
	pvs, err := pvInfo.List()
	if err != nil {
		return ret, err
	}

	pathMap := make(map[string]bool)
	for _, pv := range pvs {
		if IsCommonHostPathPV(pv) {
			pvInfos, err := GetHostPathPVMountInfoList(pv)
			if err != nil {
				return ret, fmt.Errorf("get pv %s mount info err:%v", pv.Name, err)
			}
			capacity, _ := GetHostPathPVCapacity(pv)
			for _, info := range pvInfos {
				if info.NodeName == nodeName {
					for _, mountInfo := range info.MountInfos {
						mp := path.Clean(mountInfo.HostPath)
						if _, exist := pathMap[mp]; exist == false {
							pathMap[mp] = true
							ret.MountInfos = append(ret.MountInfos, mountInfo)
						}
					}
					if podMaps, err := GetHostPathPVUsedPodMap(pv, podInfo, nodeName); err != nil {
						return ret, fmt.Errorf("GetHostPathPVUsedPodMap of pv %s on node %s err:%v", pv.Name, nodeName, err)
					} else {
						if IsSharedHostPathPV(pv) {
							if len(info.MountInfos) == 0 && len(podMaps) > 0 {
								ret.MountInfos = append(ret.MountInfos, hostpath.MountInfo{
									HostPath:        "",
									VolumeQuotaSize: capacity,
								})
								glog.V(4).Infof("share pv %s on node %s has no mount info has has some pod on it", pv.Name, nodeName)
							}
						} else if len(podMaps) > len(info.MountInfos) { // some pod not update it's quota path info
							for i := 0; i < len(podMaps)-len(info.MountInfos); i++ {
								ret.MountInfos = append(ret.MountInfos, hostpath.MountInfo{
									HostPath:        "",
									VolumeQuotaSize: capacity,
								})
							}
							glog.V(4).Infof("share pv %s on node %s has %d mount info but run pod %d", pv.Name, nodeName, len(info.MountInfos), len(podMaps))
						}
					}
					break
				}
			}
		}
	}
	return ret, nil
}

func GetHostPathPVMountInfoList(pv *v1.PersistentVolume) (hostpath.HostPathPVMountInfoList, error) {
	if IsCommonHostPathPV(pv) && pv.Annotations != nil && pv.Annotations[common.PVVolumeHostPathMountNode] != "" {
		mountInfo := pv.Annotations[common.PVVolumeHostPathMountNode]
		mountList := hostpath.HostPathPVMountInfoList{}
		errUmarshal := json.Unmarshal([]byte(mountInfo), &mountList)
		if errUmarshal != nil {
			return nil, errUmarshal
		}
		return mountList, nil
	}
	return nil, nil
}

func IsHostPathPVMountOnNode(pv *v1.PersistentVolume, nodeName string) bool {
	mountList, err := GetHostPathPVMountInfoList(pv)
	if err == nil && mountList != nil {
		for _, info := range mountList {
			if info.NodeName == nodeName {
				return true
			}
		}
	}
	return false
}

func GetHostPathPVCapacity(pv *v1.PersistentVolume) (int64, error) {
	if IsCommonHostPathPV(pv) {
		storage, ok := pv.Spec.Capacity[v1.ResourceStorage]
		capacity := storage.Value()
		if pv.Annotations != nil && pv.Annotations[common.PVHostPathCapacityAnn] != "" {
			capacityAnn, errParse := strconv.ParseInt(pv.Annotations[common.PVHostPathCapacityAnn], 10, 64)
			if errParse == nil {
				capacity = capacityAnn
			}
		} else {
			if ok == false {
				return 0, fmt.Errorf("pv %s's Capacity is not define", pv.Name)
			}
		}

		return capacity, nil
	}
	return 0, nil
}

func SetHostpathPVCapacity(pv *v1.PersistentVolume, size int64) error {
	if size < 10 {
		return fmt.Errorf("set pv size should not less than 10M")
	}
	if IsCommonHostPathPV(pv) {
		if pv.Annotations == nil {
			pv.Annotations = make(map[string]string)
		}
		pv.Annotations[common.PVHostPathCapacityAnn] = fmt.Sprintf("%d", size)
		return nil
	}
	return fmt.Errorf("pv %s is not hostpath pv", pv.Name)
}
