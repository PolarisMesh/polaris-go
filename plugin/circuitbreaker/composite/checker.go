/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package composite

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	pb "github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	defaultCheckInterval = 10 * time.Second
)

type ResourceHealthChecker struct {
	resource       model.Resource
	faultDetector  *fault_tolerance.FaultDetector
	stopped        int32
	healthCheckers map[string]healthcheck.HealthChecker
	circuitBreaker *CompositeCircuitBreaker
	// regexFunction
	regexFunction func(string) *regexp.Regexp
	// cancels
	cancels []context.CancelFunc
	// lock
	lock sync.RWMutex
	//
	instances map[string]*ProtocolInstance
	// instanceExpireIntervalMill .
	instanceExpireIntervalMill int64
	//
	log log.Logger
}

func NewResourceHealthChecker(res model.Resource, faultDetector *fault_tolerance.FaultDetector,
	breaker *CompositeCircuitBreaker) *ResourceHealthChecker {
	checker := &ResourceHealthChecker{
		resource:       res,
		faultDetector:  faultDetector,
		circuitBreaker: breaker,
		regexFunction: func(s string) *regexp.Regexp {
			return breaker.loadOrStoreCompiledRegex(s)
		},
		healthCheckers: breaker.healthCheckers,
		cancels:        make([]context.CancelFunc, 0, 4),
		instances:      make(map[string]*ProtocolInstance, 16),
	}
	if insRes, ok := res.(*model.InstanceResource); ok {
		checker.addInstance(insRes, false)
	}
	checker.start()
	return checker
}

func (c *ResourceHealthChecker) start() {
	protocol2Rules := c.selectFaultDetectRules(c.resource, c.faultDetector)
	for protocol, rule := range protocol2Rules {
		checkFunc := c.createCheckJob(protocol, rule)
		interval := defaultCheckInterval
		if rule.GetInterval() > 0 {
			interval = time.Duration(rule.GetInterval()) * time.Second
		}
		c.log.Infof("[CircuitBreaker] schedule task: resource=%s, protocol=%s, interval=%+v, rule=%s",
			c.resource.String(), protocol, interval, rule.GetName())
		ctx, cancel := context.WithCancel(context.Background())
		c.cancels = append(c.cancels, cancel)
		go func(ctx context.Context, f func()) {
			ticker := time.NewTicker(interval)
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
				case <-ticker.C:
					f()
				}
			}
		}(ctx, checkFunc)
	}
	if c.resource.GetLevel() != fault_tolerance.Level_INSTANCE {
		checkPeriod := c.circuitBreaker.checkPeriod
		c.log.Infof("[CircuitBreaker] schedule expire task: resource=%s, interval=%+v", c.resource.String(), checkPeriod)
		ctx, cancel := context.WithCancel(context.Background())
		c.cancels = append(c.cancels, cancel)
		go func(ctx context.Context, f func()) {
			ticker := time.NewTicker(checkPeriod)
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
				case <-ticker.C:
					f()
				}
			}
		}(ctx, c.cleanInstances)
	}
}

func (c *ResourceHealthChecker) stop() {
	c.log.Infof("[CircuitBreaker] health checker for resource=%s has stopped", c.resource.String())
	atomic.StoreInt32(&c.stopped, 1)
	for i := range c.cancels {
		c.cancels[i]()
	}
}

func (c *ResourceHealthChecker) isStopped() bool {
	return atomic.LoadInt32(&c.stopped) == 1
}

func (c *ResourceHealthChecker) cleanInstances() {
	curTimeMill := time.Now().UnixMilli()
	expireIntervalMill := c.instanceExpireIntervalMill

	waitDel := make([]string, 0, 4)
	func() {
		c.lock.RLock()
		defer c.lock.RUnlock()

		for k, v := range c.instances {
			if v.isCheckSuccess() {
				continue
			}
			lastReportMilli := v.getLastReportMilli()
			if curTimeMill-lastReportMilli >= expireIntervalMill {
				waitDel = append(waitDel, k)
				c.log.Infof("[CircuitBreaker] clean instance from health check tasks, resource=%s, expired node=%s, lastReportMilli=%d",
					c.resource.String(), k, lastReportMilli)
			}
		}
	}()

	c.lock.Lock()
	defer c.lock.Unlock()
	for k := range waitDel {
		delete(c.instances, waitDel[k])
	}
}

func (c *ResourceHealthChecker) createCheckJob(protocol string, rule *fault_tolerance.FaultDetectRule) func() {
	return func() {
		if c.isStopped() {
			return
		}
		c.checkResource(protocol, rule)
	}
}

func (c *ResourceHealthChecker) checkResource(protocol fault_tolerance.FaultDetectRule_Protocol, rule *fault_tolerance.FaultDetectRule) {
	port := rule.GetPort()
	if port > 0 {
		hosts := map[string]struct{}{}
		c.lock.RLock()
		defer c.lock.RUnlock()
		for k, v := range c.instances {
			if _, ok := hosts[k]; ok {
				continue
			}
			hosts[k] = struct{}{}
			ins := pb.NewInstanceInProto(&service_manage.Instance{
				Host: wrapperspb.String(v.insRes.Node.Host),
				Port: wrapperspb.UInt32(v.insRes.Node.Port),
			}, defaultServiceKey(v.insRes.GetService()), nil)
			isSuccess := c.doCheck(ins, v.protocol, rule)
			v.setCheckResult(isSuccess)
		}
		return
	}
	c.lock.RLock()
	defer c.lock.RUnlock()
	for _, v := range c.instances {
		curProtocol := v.protocol
		if !(curProtocol == fault_tolerance.FaultDetectRule_UNKNOWN || curProtocol == protocol) {
			continue
		}
		ins := pb.NewInstanceInProto(&service_manage.Instance{
			Host: wrapperspb.String(v.insRes.Node.Host),
			Port: wrapperspb.UInt32(v.insRes.Node.Port),
		}, defaultServiceKey(v.insRes.GetService()), nil)
		isSuccess := c.doCheck(ins, v.protocol, rule)
		v.setCheckResult(isSuccess)
	}
}

func (c *ResourceHealthChecker) doCheck(ins model.Instance, protocol fault_tolerance.FaultDetectRule_Protocol, rule *fault_tolerance.FaultDetectRule) bool {
	checker, ok := c.healthCheckers[strings.ToLower(protocol.String())]
	if !ok {
		c.log.Infof("plugin not found, skip health check for instance=%s:%d, resource=%s, protocol=%s",
			ins.GetHost(), ins.GetPort(), c.resource.String(), protocol.String())
		return false
	}
	ret, err := checker.DetectInstance(ins)
	if err != nil {
		return false
	}
	stat := &model.ResourceStat{
		Resource:  c.resource,
		RetCode:   ret.GetCode(),
		Delay:     ret.GetDelay(),
		RetStatus: ret.GetRetStatus(),
	}
	c.circuitBreaker.Report(stat)
	return stat.RetStatus == model.RetSuccess
}

func (c *ResourceHealthChecker) addInstance(res *model.InstanceResource, record bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	saveIns, ok := c.instances[res.Node.String()]
	if !ok {
		c.instances[res.Node.String()] = &ProtocolInstance{
			protocol:        parseProtocol(res.Protocol),
			insRes:          res,
			lastReportMilli: time.Now().UnixMilli(),
		}
		return
	}
	if record {
		saveIns.doReport()
	}
}

func (c *ResourceHealthChecker) sortFaultDetectRules(srcRules []*fault_tolerance.FaultDetectRule) []*fault_tolerance.FaultDetectRule {
	rules := make([]*fault_tolerance.FaultDetectRule, 0, len(srcRules))
	copy(rules, srcRules)
	sort.Slice(rules, func(i, j int) bool {
		rule1 := rules[i]
		rule2 := rules[j]

		targetSvc1 := rule1.GetTargetService()
		destNamespace1 := targetSvc1.GetNamespace()
		destService1 := targetSvc1.GetService()
		destMethod1 := targetSvc1.GetMethod().GetValue().GetValue()

		targetSvc2 := rule2.GetTargetService()
		destNamespace2 := targetSvc2.GetNamespace()
		destService2 := targetSvc2.GetService()
		destMethod2 := targetSvc2.GetMethod().GetValue().GetValue()

		if v := compareService(destNamespace1, destService1, destNamespace2, destService2); v != 0 {
			return v < 0
		}
		return compareStringValue(destMethod1, destMethod2) < 0
	})
	return rules
}

func compareService(ns1, svc1, ns2, svc2 string) int {
	if v := compareStringValue(ns1, ns2); v != 0 {
		return v
	}
	return compareStringValue(svc1, svc2)
}

func compareStringValue(v1, v2 string) int {
	isMatchAllV1 := match.IsMatchAll(v1)
	isMatchAllV2 := match.IsMatchAll(v2)

	if isMatchAllV1 && isMatchAllV2 {
		return 0
	}
	if isMatchAllV1 {
		return 1
	}
	if isMatchAllV2 {
		return -1
	}
	return strings.Compare(v1, v2)
}

func (c *ResourceHealthChecker) selectFaultDetectRules(res model.Resource, faultDetector *fault_tolerance.FaultDetector) map[string]*fault_tolerance.FaultDetectRule {
	sortedRules := c.sortFaultDetectRules(faultDetector.GetRules())
	matchRule := map[string]*fault_tolerance.FaultDetectRule{}

	for i := range sortedRules {
		rule := sortedRules[i]
		targetService := rule.GetTargetService()
		if !match.MatchService(res.GetService(), targetService.Namespace, targetService.Service) {
			continue
		}
		if res.GetLevel() == fault_tolerance.Level_METHOD {
			if !matchMethod(res, targetService.GetMethod(), c.regexFunction) {
				continue
			}
		} else {
			if !match.IsMatchAll(targetService.GetMethod().GetValue().Value) {
				continue
			}
		}
		if _, ok := matchRule[rule.GetProtocol().String()]; !ok {
			matchRule[rule.GetProtocol().String()] = rule
		}
	}
	return matchRule
}

func matchMethod(res model.Resource, val *apimodel.MatchString, regexFunc func(string) *regexp.Regexp) bool {
	if res.GetLevel() != fault_tolerance.Level_METHOD {
		return true
	}
	methodRes := res.(*model.MethodResource)
	return match.MatchString(methodRes.Method, val, regexFunc)
}

type ProtocolInstance struct {
	protocol        fault_tolerance.FaultDetectRule_Protocol
	insRes          *model.InstanceResource
	lastReportMilli int64
	checkSuccess    int32
}

func (p *ProtocolInstance) getLastReportMilli() int64 {
	return atomic.LoadInt64(&p.lastReportMilli)
}

func (p *ProtocolInstance) isCheckSuccess() bool {
	return atomic.LoadInt32(&p.checkSuccess) == 1
}

func (p *ProtocolInstance) setCheckResult(v bool) {
	if v {
		atomic.StoreInt32(&p.checkSuccess, 1)
	} else {
		atomic.StoreInt32(&p.checkSuccess, 0)
	}
}

func (p *ProtocolInstance) doReport() {
	atomic.StoreInt64(&p.lastReportMilli, time.Now().UnixMilli())
}

func parseProtocol(s string) fault_tolerance.FaultDetectRule_Protocol {
	s = strings.ToLower(s)
	if s == "http" || strings.HasPrefix(s, "http/") || strings.HasSuffix(s, "/http") {
		return fault_tolerance.FaultDetectRule_HTTP
	}
	if s == "udp" || strings.HasPrefix(s, "udp/") || strings.HasSuffix(s, "/udp") {
		return fault_tolerance.FaultDetectRule_UDP
	}
	if s == "tcp" || strings.HasPrefix(s, "tcp/") || strings.HasSuffix(s, "/tcp") {
		return fault_tolerance.FaultDetectRule_TCP
	}
	return fault_tolerance.FaultDetectRule_UNKNOWN
}

func defaultServiceKey(v *model.ServiceKey) *model.ServiceKey {
	if v == nil {
		return &model.ServiceKey{}
	}
	return v
}
