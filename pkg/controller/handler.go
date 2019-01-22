package controller

import (
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/hudl/fargo"
	core_v1 "k8s.io/api/core/v1"
)

// Handler interface contains the methods that are required
type Handler interface {
	Init() error
	ObjectCreated(keyRaw string, obj interface{})
	ObjectDeleted(keyRaw string, obj interface{})
	ObjectUpdated(keyRaw string, objOld, objNew interface{})
}

type EurekaSyncer struct {
	eureka   fargo.EurekaConnection
	liveChan chan *fargo.Instance
	deadChan chan string
}

// Init handles any handler initialization
func (e *EurekaSyncer) Init() error {
	log.Info("EurekaSyncer.Init")
	e.eureka = fargo.NewConn("http://127.0.0.1:8080/eureka/v2")
	e.liveChan = make(chan *fargo.Instance)
	e.deadChan = make(chan string)
	go e.beat()
	return nil
}

func (e *EurekaSyncer) ObjectCreated(keyRaw string, obj interface{}) {
	log.Info("EurekaSyncer.ObjectCreated")
	e.reconcile(keyRaw, obj)
}

func (e *EurekaSyncer) ObjectDeleted(keyRaw string, obj interface{}) {
	log.Info("EurekaSyncer.ObjectDeleted")
	e.reconcile(keyRaw, obj)
}

func (e *EurekaSyncer) ObjectUpdated(keyRaw string, objOld, objNew interface{}) {
	log.Info("EurekaSyncer.ObjectUpdated")
	e.reconcile(keyRaw, objNew)
}

func (e *EurekaSyncer) reconcile(keyRaw string, obj interface{}) {
	if obj == nil {
		e.deadChan <- keyRaw
		return
	}
	pod := obj.(*core_v1.Pod)
	if pod.Status.Phase == "Running" {
		log.Infof("Instance running %s", keyRaw)
		e.liveChan <- &fargo.Instance{
			UniqueID: func(i fargo.Instance) string {
				return pod.Name
			},
			App:      pod.Labels["app"],
			HostName: pod.Name,
			// TODO: set the service DNS here (can we? Or a VIP?)
			IPAddr:           pod.Status.PodIP,
			VipAddress:       pod.Status.PodIP,
			SecureVipAddress: pod.Status.PodIP,
			Status:           fargo.UP,
			Port:             8080,
			DataCenterInfo:   fargo.DataCenterInfo{Name: fargo.MyOwn},
		}
		return
	}

	log.Infof("-> Phase: %+v", pod.Status.Phase)
}

func (e *EurekaSyncer) beat() {
	instances := map[string]*fargo.Instance{}
	tickChan := time.NewTicker(time.Second * 10).C
	for {
		select {
		case _ = <-tickChan:
			log.Infof("Tick %d", len(instances))
			for _, i := range instances {
				log.Infof("Heartbeat for %s: %s", i.HostName, i.IPAddr)
				e.eureka.RegisterInstance(i)
			}
		case i := <-e.liveChan:
			key := i.UniqueID(*i)
			log.Infof("Live %s", key)
			instances[key] = i
			e.eureka.RegisterInstance(i)
		case key := <-e.deadChan:
			log.Infof("Dead %s", key)
			// The key is in the form $namespace/$pod_name, and it looks
			// like Eureka isn't able to handle / in the instance id.
			// It's either this, or s/\//_/ in the registration (which
			// maybe looks cleaner.  Reconsider sometime.)
			i := instances[strings.Split(key, "/")[1]]
			if i != nil {
				e.eureka.DeregisterInstance(i)
			}
			delete(instances, key)
		}
	}
}
