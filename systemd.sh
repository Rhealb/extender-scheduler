#!/bin/bash
logpath=/var/log/kubernetes/
certdir=/etc/kubernetes/enndata-scheduler
configdir=/etc/kubernetes/
binmovepath=$1
shift 1
svcmovepath=$1
shift 1
user=$1
shift 1
hosts=$*

fMoveFile()
{
    file=$1
    toNode=$2
    toPath=$3
    echo "scp $file to $toNode ~/$file"
    scp $file $user@$toNode:~
    echo "ssh to $toNode move ~/$file to $toPath/$file"
    ssh $user@$toNode "sudo mv ~/$file $toPath/$file" 
}

fMoveDir()
{
    dir=$1
    toNode=$2
    toDir=$3
    echo "scp -r $dir to $toNode ~/$dir"
    scp -r $dir $user@$toNode:~/
    echo "ssh to $toNode move ~/$dir to $toDir/$dir"
    ssh $user@$toNode "sudo rm -rf $toDir/$dir" 
    ssh $user@$toNode "sudo mv ~/$dir $toDir/$dir" 
}

fMakeDir() {
    dir=$1
    node=$2
    echo "mkdir $dir on node $node"
    ssh $user@$node "sudo mkdir -p $dir"
}

fStartServer() {
    name=$1
    node=$2
    echo "systemctl start $name"
    ssh $user@$node "sudo systemctl daemon-reload"
    ssh $user@$node "sudo systemctl restart $name"
}

for host in $hosts
do
    fMakeDir $binmovepath $host
    fMoveFile enndata-scheduler $host $binmovepath
    fMakeDir $svcmovepath $host
    fMoveFile enndata-scheduler.service $host $svcmovepath
    
    fMakeDir $logpath/enndata-scheduler $host
    #fMakeDir $certdir $host
    #fMoveDir tls-certs $host $certdir
    fStartServer enndata-scheduler $host
    fStartServer kube-scheduler $host || fStartServer scheduler $host
done 