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

package serviceinfo

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/tencent/pb/v1"
)

const (
	serviceStatus   = 0
	routingStatus   = 1
	rateLimitStatus = 2
)

// statusNode 一个版本变化节点
type statusNode struct {
	changeSeq  uint32
	changeTime time.Time
	changeData interface{}
	next       *statusNode
}

// statusList 一个实例或者路由信息的变化列表
type statusList struct {
	head  *statusNode
	tail  *statusNode
	count uint32
	seq   uint32
	lock  uint32
}

// statusHistory 一个服务的变化历史
type statusHistory struct {
	histories      [3]*statusList
	rateLimitRules *sync.Map
	lastUpdateTime atomic.Value
}

// meshStatusHistory 一个网格配置的变化历史
type meshStatusHistory struct {
	history        *statusList
	meshResources  *sync.Map
	lastUpdateTime atomic.Value
}

// meshKey 一个网格配置集合的标识
type meshKey struct {
	meshID  string
	typeUrl string
}

// circuitBreakNode 熔断变化节点
type circuitBreakNode struct {
	status monitorpb.StatusChange
	reason string
}

// serviceRecoverAllMap 记录一个服务下面的全死全活记录
type serviceRecoverAllMap struct {
	changeList     *statusList
	clusterRecords *sync.Map
}

// clusterRecoverAllCheck serviceRecoverAllMap[clusterKey]，一个服务下面某个cluster的全死全活检验
type clusterRecoverAllCheck struct {
	lastCheckTime atomic.Value
	currentStatus uint32
	clusterInfo   string
}

// recoverAllChange 一次全死全活变化
type recoverAllChange struct {
	statusChange monitorpb.RecoverAllStatus
	clusterInfo  string
	reason       string
}

// createCircuitBreakNode 根据熔断状态变化，创建一个变化节点
func createCircuitBreakNode(pre, current model.CircuitBreakerStatus) (*circuitBreakNode, error) {
	res := &circuitBreakNode{}
	res.reason = current.GetCircuitBreaker()
	switch current.GetStatus() {
	case model.Open:
		if nil == pre {
			res.status = monitorpb.StatusChange_CloseToOpen
		} else if pre.GetStatus() == model.HalfOpen {
			res.status = monitorpb.StatusChange_HalfOpenToOpen
		} else if pre.GetStatus() == model.Close {
			res.status = monitorpb.StatusChange_CloseToOpen
		} else {
			return nil, fmt.Errorf("invalid pre cb status for the current status %v", model.Open)
		}
	case model.HalfOpen:
		if nil != pre && pre.GetStatus() == model.Open {
			res.status = monitorpb.StatusChange_OpenToHalfOpen
		} else {
			return nil, fmt.Errorf("invalid pre cb status for the current status %v", model.Open)
		}
	case model.Close:
		if nil != pre && pre.GetStatus() == model.HalfOpen {
			res.status = monitorpb.StatusChange_HalfOpenToClose
		} else {
			return nil, fmt.Errorf("invalid pre cb status for the current status %v", model.Open)
		}
	}
	return res, nil
}

// newStatusHistory 创建新的状态历史
func newStatusHistory(currentTime time.Time) *statusHistory {
	res := &statusHistory{}
	res.histories[serviceStatus] = newStatusList()
	res.histories[routingStatus] = newStatusList()
	res.histories[rateLimitStatus] = newStatusList()
	res.rateLimitRules = &sync.Map{}
	res.lastUpdateTime.Store(currentTime)
	return res
}

// newStatusList 创建新的状态列表
func newStatusList() *statusList {
	res := &statusList{}
	res.head = &statusNode{}
	res.tail = res.head
	return res
}

// addStatus 添加一个版本号状态
func (s *statusList) addStatus(data interface{}, currentTime time.Time) {
	currentSeq := atomic.AddUint32(&s.seq, 1)
	newNode := &statusNode{
		changeSeq:  currentSeq,
		changeTime: currentTime,
		changeData: data,
	}
	for {
		if !atomic.CompareAndSwapUint32(&s.lock, 0, 1) {
			continue
		}
		s.tail.next = newNode
		s.tail = newNode
		s.count++
		atomic.StoreUint32(&s.lock, 0)
		return
	}
}

// addDeleteStatus 添加一个删除状态
func (s *statusList) addDeleteStatus(data interface{}, currentTime time.Time) {
	currentSeq := atomic.AddUint32(&s.seq, 1)
	newNode := &statusNode{
		changeSeq:  currentSeq,
		changeTime: currentTime,
		changeData: data,
	}
	for {
		if !atomic.CompareAndSwapUint32(&s.lock, 0, 1) {
			continue
		}
		s.tail.next = newNode
		s.tail = newNode
		s.count++
		atomic.StoreUint32(&s.lock, 0)
		// 如果这个实例或者路由信息被删除了，重置seq
		atomic.CompareAndSwapUint32(&s.seq, currentSeq, 0)
		return
	}
}

// getNodes 返回当前历史状态节点，并且将节点置空
// 返回当前seq，判断是不是信息已经被
// 返回当前状态列表长度
func (s *statusList) getNodes() (n *statusNode, currentSeq uint32, currentCount uint32) {
	for {
		if !atomic.CompareAndSwapUint32(&s.lock, 0, 1) {
			continue
		}
		n = s.head.next
		s.tail = s.head
		s.head.next = nil
		currentSeq = atomic.LoadUint32(&s.seq)
		currentCount = s.count
		s.count = 0
		atomic.StoreUint32(&s.lock, 0)
		return
	}
}