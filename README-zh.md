# Enndata scheduler
　
## 说明

[English](README.md) | [中文](README-zh.md)

**为了满足一些应用对本地磁盘的访问需求(如: HDFS等)，我们设计了一个本地磁盘管理及调度系统．该系统主要分为２个部分第一部分就是接下来要介绍的调度模块，第二部分是[本地磁盘管理模块](https://gitlab.cloud.enndata.cn/kubernetes/k8s-plugins/tree/master/csi-plugin/hostpathpv/README-zh.md)．当然本模块不仅仅包括本地磁盘调度相关模块（Predicates: hostpathpvdiskpressure, hostpathpvaffinity; Priorities: hostpathpvdiskuse, hostpathpvspread), 还包括一些我们自定义的一些调度策略如: Predicate namespacenodeselector.**

## 部署
+ **1)下载代码及编译：**

        $git clone ssh://git@gitlab.cloud.enndata.cn:10885/kubernetes/k8s-plugins.git
        $cd k8s-plugins/extender-scheduler/
        $make release REGISTRY=10.19.140.200:29006
    
        （make release 将编译代码且制作相应docker image 10.19.140.200:29006/library/k8s-scheduler:$(TAG) , 并将其push到registry．也可以只执行make build 生成enndata-scheduler可执行文件，详情可以查看该目录下的Makefile）
	  
+ **2)部署：**

　　该调度器支持Deployment(基于Pod容器)和Systemd两种部署方式（目前基于可靠性和依赖关系考虑我们主要还是采用Systemd的方式）．

+ **2.1)Systemd部署：**

	    $cp scheduler-policy.json /etc/kubernetes/scheduler-policy.json 
        （kube-scheduler加参数--policy-config-file=/etc/kubernetes/scheduler-config.json，如果没有做haproxy映射只是在本地测试集群部署需要将scheduler-policy.json里的6445端口改为6600端口，如做了haproxy配置需要在haproxy配置文件添加如下配置：
        frontend k8s_https_6445
        　　bind 127.0.0.1:6445
        　　mode tcp
        　　default_backend k8s_enndata_scheduler
        backend k8s_enndata_scheduler
        　　mode tcp
        　　balance roundrobin
        　　default-server inter 5s fall 1 rise 2
        　　server k8s-master1 10.19.137.140:6600 check
        　　server k8s-master2 10.19.137.141:6600 check
        　　server k8s-master3 10.19.137.142:6600 check
          ）

	    $make systemd MASTERS=10.19.137.140 MASTERUSER=core
        (如果是本地部署可以用：make systemd或者make systemd MASTERS=127.0.0.1 MASTERUSER=root)

	    $systemctl status enndata-scheduler #确认enndata-scheduler service是否启动成功．

+ **2.2)Deployment部署：**

　　Deployment部署是通过部署一个叫enndata-scheduler的调度器来实现的，它只会调度pod.spec.schedulerName == enndata-scheduler的Pod, 所以要想将使用hostpathpv的Pod交给enndata-scheduler调度器就必须在创建Pod的时候定义schedulerName或者部署一个admission-controller hook来自动判断Pod是否使用了hostpathpv．基于此我们也实现了一个该admission hook [hostpathpvresource](https://gitlab.cloud.enndata.cn/kubernetes/k8s-plugins/blob/master/admission-controller/pkg/hostpathpvresource/README-zh.md), 在部署enndata-scheduler之前先部署hostpathpvresource：

	    $make install REGISTRY=127.0.0.1:29006

+ **2.3)nsnodeselector https server部署：**

　　该服务主要是用来配置各个namespace的Pod可以被调度到哪些Node的后端服务，需要配合自定义的predicate策略namespacenodeselector一起使用．该服务可以和scheduler一起进行部署合作可以单独部署通过参数--nodeselector-server-only来控制．

	    $make install-nsnodeselector  REGISTRY=127.0.0.1:29006
        (部署成功之后可以通过https://127.0.0.1:29111/nsnodeselector　来对进行配置默认用户名秘密 zhtsC1002 : zhtsC1002, 也可以通过修改gencerts.sh里的BASIC_AUTH来更改)

## 测试
关于hostpathpv调度的测试可以参考[CSI hostpathpv](https://gitlab.cloud.enndata.cn/kubernetes/k8s-plugins/tree/master/csi-plugin/hostpathpv/README-zh.md)测试．下面主要介绍nsnodeselector的测试：

	$kubectl get node  #查看当前节点情况
    NAME                  STATUS    ROLES      AGE       VERSION
    10.19.138.197       Ready     <none>     60d       v1.11.2-26+7bf778a61b0a47-dirty
    192.168.122.196   Ready     hostpath   60d       v1.11.2-21+84c24b0e53e9f3-dirty
    192.168.122.9       Ready     hostpath   60d       v1.11.2-21+84c24b0e53e9f3-dirty

	$curl -k -u zhtsC1002:zhtsC1002 https://127.0.0.1:29111/nsnodeselector/add?namespace=patricktest\&type=match\&value=192.168.122.9\&key=kubernetes.io/hostname
	{
        "code": 0,
        "msg": "OK",
        "data": ""
    }
	$cat pod.yaml
	apiVersion: v1
    kind: Pod
    metadata:
        name: nsnodeselectortest
        namespace: patricktest
    spec:
        containers:
        - name: test
          image: busybox
          command:
          - "sleep"
          - "1000000000"
          resources:
            limits:
                cpu: 200m
                memory: 256Mi
            requests:
                cpu: 200m

	$kubectl create -f pod.yaml

	$kubectl get pod nsnodeselectortest -o wide # pod只会被调度到192.168.122.9节点
	NAME                 READY     STATUS    RESTARTS   AGE       IP           NODE            NOMINATED NODE
    nsnodeselectortest   1/1       Running     0        1m        10.9.6.14    192.168.122.9   <none>


## 调度策略介绍
+ **1) Predicate策略hostpathpvdiskpressure：**

  该策略主要是判断节点是否有足够的hostpath可以用于调度该Pod所引用的hostpath, 如果是keep PV且该node上有没被其他Pod使用的目录不会计算在内．

+ **2) Predicate策略hostpathpvaffinity：**

  该策略主要是在Pod重启之后如果引用的PV是keep策略的话会被调度到之前的Node上．

+ **3) Predicate策略namespacenodeselector：**

  该策略主要是规划某个Namespace的Pod可以被调度到哪些node, 可以将其看作是Namespace的nodeselector．

+ **4) Prioritie策略hostpathpvdiskuse：**

  该策略主要是将Pod调度到hostpath quota负载比较低的Node上，避免一些Node的hostpath quota用完了，而另一些Node quota没怎么用．

+ **5) Prioritie策略hostpathpvspread：**

  该策略主要是将使用同一个PV的不同Pod调度到不同Node上使应用尽可能的使用磁盘的IO, 同时也是为了避免因为一个Node down机之后应用数据不可用的问题．
　　　