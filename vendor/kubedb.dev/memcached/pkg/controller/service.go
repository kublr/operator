/*
Copyright The KubeDB Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"

	api "kubedb.dev/apimachinery/apis/kubedb/v1alpha1"
	"kubedb.dev/apimachinery/pkg/eventer"

	"github.com/appscode/go/log"
	core "k8s.io/api/core/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	kutil "kmodules.xyz/client-go"
	core_util "kmodules.xyz/client-go/core/v1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ofst "kmodules.xyz/offshoot-api/api/v1"
)

var defaultDBPort = core.ServicePort{
	Name:       "db",
	Protocol:   core.ProtocolTCP,
	Port:       11211,
	TargetPort: intstr.FromString("db"),
}

func (c *Controller) ensureService(memcached *api.Memcached) (kutil.VerbType, error) {
	// Check if service name exists
	if err := c.checkService(memcached, memcached.ServiceName()); err != nil {
		return kutil.VerbUnchanged, err
	}
	// create database Service
	vt, err := c.createService(memcached)
	if err != nil {
		return kutil.VerbUnchanged, err
	} else if vt != kutil.VerbUnchanged {
		c.recorder.Eventf(
			memcached,
			core.EventTypeNormal,
			eventer.EventReasonSuccessful,
			"Successfully %s Service",
			vt,
		)
	}
	return vt, nil
}

func (c *Controller) checkService(memcached *api.Memcached, serviceName string) error {
	service, err := c.Client.CoreV1().Services(memcached.Namespace).Get(serviceName, metav1.GetOptions{})
	if err != nil {
		if kerr.IsNotFound(err) {
			return nil
		}
		return err
	}

	if service.Labels[api.LabelDatabaseKind] != api.ResourceKindMemcached ||
		service.Labels[api.LabelDatabaseName] != memcached.Name {
		return fmt.Errorf(`intended service "%v/%v" already exists`, memcached.Namespace, serviceName)
	}

	return nil
}

func (c *Controller) createService(memcached *api.Memcached) (kutil.VerbType, error) {
	meta := metav1.ObjectMeta{
		Name:      memcached.OffshootName(),
		Namespace: memcached.Namespace,
	}

	owner := metav1.NewControllerRef(memcached, api.SchemeGroupVersion.WithKind(api.ResourceKindMemcached))

	_, ok, err := core_util.CreateOrPatchService(c.Client, meta, func(in *core.Service) *core.Service {
		core_util.EnsureOwnerReference(&in.ObjectMeta, owner)
		in.Labels = memcached.OffshootLabels()
		in.Annotations = memcached.Spec.ServiceTemplate.Annotations

		in.Spec.Selector = memcached.OffshootSelectors()
		in.Spec.Ports = ofst.MergeServicePorts(
			core_util.MergeServicePorts(in.Spec.Ports, []core.ServicePort{defaultDBPort}),
			memcached.Spec.ServiceTemplate.Spec.Ports,
		)

		if memcached.Spec.ServiceTemplate.Spec.ClusterIP != "" {
			in.Spec.ClusterIP = memcached.Spec.ServiceTemplate.Spec.ClusterIP
		}
		if memcached.Spec.ServiceTemplate.Spec.Type != "" {
			in.Spec.Type = memcached.Spec.ServiceTemplate.Spec.Type
		}
		in.Spec.ExternalIPs = memcached.Spec.ServiceTemplate.Spec.ExternalIPs
		in.Spec.LoadBalancerIP = memcached.Spec.ServiceTemplate.Spec.LoadBalancerIP
		in.Spec.LoadBalancerSourceRanges = memcached.Spec.ServiceTemplate.Spec.LoadBalancerSourceRanges
		in.Spec.ExternalTrafficPolicy = memcached.Spec.ServiceTemplate.Spec.ExternalTrafficPolicy
		if memcached.Spec.ServiceTemplate.Spec.HealthCheckNodePort > 0 {
			in.Spec.HealthCheckNodePort = memcached.Spec.ServiceTemplate.Spec.HealthCheckNodePort
		}
		return in
	})
	return ok, err
}

func (c *Controller) ensureStatsService(memcached *api.Memcached) (kutil.VerbType, error) {
	// return if monitoring is not prometheus
	if memcached.GetMonitoringVendor() != mona.VendorPrometheus {
		log.Infoln("spec.monitor.agent is not coreos-operator or builtin.")
		return kutil.VerbUnchanged, nil
	}

	// Check if Stats Service name exists
	if err := c.checkService(memcached, memcached.StatsService().ServiceName()); err != nil {
		return kutil.VerbUnchanged, err
	}

	owner := metav1.NewControllerRef(memcached, api.SchemeGroupVersion.WithKind(api.ResourceKindMemcached))

	// reconcile stats Service
	meta := metav1.ObjectMeta{
		Name:      memcached.StatsService().ServiceName(),
		Namespace: memcached.Namespace,
	}
	_, vt, err := core_util.CreateOrPatchService(c.Client, meta, func(in *core.Service) *core.Service {
		core_util.EnsureOwnerReference(&in.ObjectMeta, owner)
		in.Labels = memcached.StatsServiceLabels()
		in.Spec.Selector = memcached.OffshootSelectors()
		in.Spec.Ports = core_util.MergeServicePorts(in.Spec.Ports, []core.ServicePort{
			{
				Name:       api.PrometheusExporterPortName,
				Protocol:   core.ProtocolTCP,
				Port:       memcached.Spec.Monitor.Prometheus.Exporter.Port,
				TargetPort: intstr.FromString(api.PrometheusExporterPortName),
			},
		})
		return in
	})
	if err != nil {
		return kutil.VerbUnchanged, err
	} else if vt != kutil.VerbUnchanged {
		c.recorder.Eventf(
			memcached,
			core.EventTypeNormal,
			eventer.EventReasonSuccessful,
			"Successfully %s stats service",
			vt,
		)
	}
	return vt, nil
}
