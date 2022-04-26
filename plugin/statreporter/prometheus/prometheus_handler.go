// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//

package prometheus

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/plugin/statreporter/prometheus/addons"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusHandler
type PrometheusHandler struct {
	//prometheus的metrics注册
	registry *prometheus.Registry
	// metrics的 http handler
	handler http.Handler
	cfg     *Config
	//
	metricVecCaches map[string]prometheus.Collector
	bindIP          string
	port            uint32
}

func newHandler(ctx *plugin.InitContext) (*PrometheusHandler, error) {
	p := &PrometheusHandler{}
	return p, p.init(ctx)
}

func (p *PrometheusHandler) init(ctx *plugin.InitContext) error {
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	if cfgValue != nil {
		p.cfg = cfgValue.(*Config)
	}

	p.metricVecCaches = make(map[string]prometheus.Collector)
	p.registry = prometheus.NewRegistry()
	if err := p.registerMetrics(); err != nil {
		return err
	}

	p.bindIP = p.cfg.IP
	if p.bindIP == "" {
		p.bindIP = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	}
	p.port = p.cfg.Port

	p.handler = &metricsHttpHandler{
		promeHttpHandler: promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{}),
		lock:             &sync.RWMutex{},
	}
	p.runInnerMetricsWebServer()

	return nil
}

func (p *PrometheusHandler) registerMetrics() error {
	for _, desc := range metrcisDesces {
		var collector prometheus.Collector
		switch desc.MetricType {
		case TypeForGaugeVec:
			collector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		case TypeForCounterVec:
			collector = prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		case TypeForMaxGaugeVec:
			collector = addons.NewMaxGaugeVec(prometheus.GaugeOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		case TypeForHistogramVec:
			collector = prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		}

		err := p.registry.Register(collector)
		if err != nil {
			log.GetBaseLogger().Errorf("register prometheus collector error, %v", err)
			return err
		}
		if _, ok := p.metricVecCaches[desc.Name]; ok {
			log.GetBaseLogger().Errorf("register prometheus collector duplicate, %s", desc.Name)
			return fmt.Errorf("register prometheus collector duplicate, %s", desc.Name)
		}
		p.metricVecCaches[desc.Name] = collector
	}
	return nil
}

func (p *PrometheusHandler) ReportStat(metricsType model.MetricType, metricsVal model.InstanceGauge) error {
	switch metricsType {
	case model.ServiceStat:
		p.handleRouterGauge(metricsType, metricsVal.(*model.ServiceCallResult))
	case model.RateLimitStat:
		p.handleRateLimitGauge(metricsType, metricsVal.(*model.RateLimitGauge))
	case model.CircuitBreakStat:
		p.handleCircuitBreakGauge(metricsType, metricsVal.(*model.CircuitBreakGauge))
	}
	return nil
}

// runInnerMetricsWebServer 启动用于 prometheus 主动拉取的 http-server，如果端口设置为负数，则不启用
func (p *PrometheusHandler) runInnerMetricsWebServer() {
	if p.port < 0 {
		return
	}

	go func() {
		address := fmt.Sprintf("%s:%d", p.bindIP, p.cfg.Port)
		log.GetBaseLogger().Infof("start metrics http-server address : %s", address)
		if err := http.ListenAndServe(address, p.GetHttpHandler()); err != nil {
			log.GetBaseLogger().Errorf("start metrics http-server fail : %s", err)
			return
		}
	}()
}

// GetHttpHandler 获取 handler
func (p *PrometheusHandler) GetHttpHandler() http.Handler {
	return p.handler
}

func (p *PrometheusHandler) handleRouterGauge(metricsType model.MetricType, val *model.ServiceCallResult) {
	labels := p.convertInsGaugeToLabels(val)

	total := p.metricVecCaches[MetricsNameUpstreamRequestTotal].(*prometheus.CounterVec)
	total.With(labels).Inc()

	success := p.metricVecCaches[MetricsNameUpstreamRequestSuccess].(*prometheus.CounterVec)
	if val.GetRetStatus() == model.RetSuccess {
		success.With(labels).Inc()
	}

	delay := val.GetDelay()
	if delay != nil {
		data := float64((*delay).Milliseconds())

		timeout := p.metricVecCaches[MetricsNameUpstreamRequestTimeout].(*prometheus.GaugeVec)
		timeout.With(labels).Add(data)

		maxTimeout := p.metricVecCaches[MetricsNameUpstreamRequestMaxTimeout].(*addons.MaxGaugeVec)
		maxTimeout.With(labels).Set(data)

		reqDelay := p.metricVecCaches[MetricsNameUpstreamRequestTimeout].(*prometheus.HistogramVec)
		reqDelay.With(labels).Observe(data)
	}

}

func (p *PrometheusHandler) handleRateLimitGauge(metricsType model.MetricType, val *model.RateLimitGauge) {
	labels := p.convertRateLimitGaugeToLabels(val)

	total := p.metricVecCaches[MetricsNameRateLimitRequestTotal].(*prometheus.CounterVec)
	total.With(labels).Inc()

	pass := p.metricVecCaches[MetricsNameRateLimitRequestPass].(*prometheus.CounterVec)
	if val.Result == model.QuotaResultOk {
		pass.With(labels).Inc()
	}

	limit := p.metricVecCaches[MetricsNameRateLimitRequestLimit].(*prometheus.CounterVec)
	if val.Result == model.QuotaResultLimited {
		limit.With(labels).Inc()
	}
}

func (p *PrometheusHandler) handleCircuitBreakGauge(metricsType model.MetricType, val *model.CircuitBreakGauge) {
	labels := p.convertCircuitBreakGaugeToLabels(val)

	open := p.metricVecCaches[MetricsNameCircuitBreakerOpen].(*prometheus.CounterVec)

	status := val.GetCircuitBreakerStatus().GetStatus()
	if status == model.Open {
		open.With(labels).Inc()
	} else {
		open.With(labels).Add(-1)
	}

	halfOpen := p.metricVecCaches[MetricsNameCircuitBreakerHalfOpen].(*prometheus.CounterVec)

	if status == model.HalfOpen {
		halfOpen.With(labels).Inc()
	} else {
		halfOpen.With(labels).Add(-1)
	}
}

func (p *PrometheusHandler) convertInsGaugeToLabels(val *model.ServiceCallResult) map[string]string {
	labels := make(map[string]string)

	for label, supplier := range InstanceGaugeLabelOrder {
		labels[label] = supplier(val)
	}

	labels[CallerIP] = p.bindIP
	return labels
}

func (p *PrometheusHandler) convertRateLimitGaugeToLabels(val *model.RateLimitGauge) map[string]string {
	labels := make(map[string]string)

	for label, supplier := range RateLimitGaugeLabelOrder {
		labels[label] = supplier(val)
	}

	labels[CallerIP] = p.bindIP
	return labels
}

func (p *PrometheusHandler) convertCircuitBreakGaugeToLabels(val *model.CircuitBreakGauge) map[string]string {
	labels := make(map[string]string)

	for label, supplier := range CircuitBreakerGaugeLabelOrder {
		labels[label] = supplier(val)
	}

	labels[CallerIP] = p.bindIP
	return labels
}