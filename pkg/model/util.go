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

package model

import (
	"errors"
	"fmt"
	"hash/crc64"
	"hash/fnv"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/duration"
	"github.com/mitchellh/go-homedir"
)

const (
	HomeVar = "$HOME"
)

// HashSet Hash集合数据结构
type HashSet map[interface{}]bool

// Add 往集合添加值
func (h HashSet) Add(value interface{}) {
	if _, ok := h[value]; !ok {
		h[value] = true
	}
}

// Delete 往集合删除值
func (h HashSet) Delete(value interface{}) bool {
	if _, ok := h[value]; ok {
		delete(h, value)
		return true
	}
	return false
}

// Contains 值是否存在集合中
func (h HashSet) Contains(value interface{}) bool {
	_, ok := h[value]
	return ok
}

// Copy 复制hashSet
func (h HashSet) Copy() HashSet {
	newSet := make(map[interface{}]bool, len(h))
	for k, v := range h {
		newSet[k] = v
	}
	return newSet
}

// NewSyncHashSet 创建协程安全的HashSet
func NewSyncHashSet() *SyncHashSet {
	return &SyncHashSet{
		values: HashSet{},
	}
}

// SyncHashSet 协程安全的HashSet
type SyncHashSet struct {
	values HashSet
	mutex  sync.Mutex
}

// Add 往set添加元素
func (s *SyncHashSet) Add(value interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.values.Add(value)
}

// Delete 删除元素
func (s *SyncHashSet) Delete(value interface{}) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.values.Delete(value)
}

// Contains 检查元素存在性
func (s *SyncHashSet) Contains(value interface{}) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.values.Contains(value)
}

// Copy 拷贝列表的元素
func (s *SyncHashSet) Copy() HashSet {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.values.Copy()
}

// IsDir file path is dir
func IsDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// IsFile file path is dir
func IsFile(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !s.IsDir()
}

// HashStr 对字符串进行hash操作
func HashStr(key string) (uint64, error) {
	a := fnv.New64()
	_, err := a.Write([]byte(key))
	if err != nil {
		return 0, err
	}
	return a.Sum64(), nil
}

// HashMessage 对PB消息进行hash
func HashMessage(message proto.Message) uint64 {
	hashCode, _ := HashStr(message.String())
	return hashCode
}

// ToDurationPtr 转换时间指针
func ToDurationPtr(v time.Duration) *time.Duration {
	return &v
}

// ToBoolPtr .
func ToBoolPtr(v bool) *bool {
	return &v
}

// ToMilliSeconds 时间转换成毫秒
func ToMilliSeconds(v time.Duration) int64 {
	return ParseMilliSeconds(v.Nanoseconds())
}

// ParseMilliSeconds 时间转换成毫秒
func ParseMilliSeconds(v int64) int64 {
	return v / 1e6
}

// GetIP get local ip from inteface name like eth1
func GetIP(name string) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, v := range ifaces {
		if v.Name == name {
			addrs, err := v.Addrs()
			if err != nil {
				return "", err
			}

			for _, addr := range addrs {
				var ip net.IP
				switch val := addr.(type) {
				case *net.IPNet:
					ip = val.IP
				case *net.IPAddr:
					ip = val.IP
				default:
					continue
				}

				if len(ip) == net.IPv6len {
					return ip.To16().String(), nil
				} else if len(ip) == net.IPv4len {
					return ip.To4().String(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("net interfaces is empty")
}

// IsNearbyMatch 判断是否满足就近条件
func IsNearbyMatch(dst, src string) bool {
	if len(dst) == 0 || len(src) == 0 {
		return true
	}
	return dst == src
}

// ReplaceHomeVar 替换相对路径
func ReplaceHomeVar(path string) string {
	if !strings.HasPrefix(path, HomeVar) {
		return path
	}
	homeDir, err := homedir.Dir()
	if err != nil {
		return strings.Replace(path, HomeVar, ".", 1)
	}
	return strings.Replace(path, HomeVar, homeDir, 1)
}

// PathExist 查看文件路径是否存在
func PathExist(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return os.IsExist(err)
	}
	return true
}

// EnsureAndVerifyDir 检测缓存目录，不存在则创建
func EnsureAndVerifyDir(dir string) error {
	if !PathExist(dir) {
		err := os.MkdirAll(dir, 0744)
		if err != nil {
			return NewSDKError(ErrCodeDiskError, err, "unable to create dir %s", dir)
		}
		return nil
	}
	pathInfo, _ := os.Stat(dir)
	if !pathInfo.IsDir() {
		return NewSDKError(ErrCodeDiskError, nil, "path %s is a file path", dir)
	}
	return nil
}

// GetErrorCodeFromError 从错误中获取错误码
func GetErrorCodeFromError(e error) ErrCode {
	if e == nil {
		return ErrCodeSuccess
	}
	sdkErr, ok := e.(SDKError)
	if !ok {
		return ErrCodeUnknown
	}
	return sdkErr.ErrorCode()
}

// IsInstanceAvailable 服务实例是否可用
func IsInstanceAvailable(instance Instance) bool {
	if !instance.IsHealthy() {
		return false
	}
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil != cbStatus && !cbStatus.IsAvailable() {
		return false
	}
	return true
}

// SortMap 对map进行排序, keys的长度必须等于map的长度
// 返回已经排序的key，以及map中总字符串长度
func SortMap(values map[string]string, keys []string) ([]string, int) {
	if len(values) == 0 {
		return keys, 0
	}
	if len(keys) < len(values) {
		keys = make([]string, len(values))
	}
	var idx int
	var count int
	for k, v := range values {
		count += len(k) + len(v)
		keys[idx] = k
		idx++
	}
	if len(keys) > 1 {
		sort.Strings(keys)
	}
	return keys, count
}

// ToNetIP 将uint32类型转化为ipv4地址
func ToNetIP(val uint32) net.IP {
	return net.IPv4(byte(val>>24), byte(val>>16&0xFF), byte(val>>8)&0xFF, byte(val&0xFF))
}

// ConvertDuration converts to golang duration and logs errors
func ConvertDuration(d *duration.Duration) (time.Duration, error) {
	if d == nil {
		return 0, nil
	}
	return d.AsDuration(), nil
}

// ProtoDurationToMS pb时间段转毫秒
func ProtoDurationToMS(dur *duration.Duration) int64 {
	timeDuration, _ := ConvertDuration(dur)
	return int64(timeDuration / time.Millisecond)
}

func GetCrc64Hash(value string) (uint64, error) {
	h := crc64.New(crc64.MakeTable(crc64.ECMA))
	_, err := h.Write([]byte(value))
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

func CheckConfigFileMetadata(configFileMetadata ConfigFileMetadata) error {
	if configFileMetadata == nil {
		return errors.New("configFileMetadata is nil")
	}

	if configFileMetadata.GetNamespace() == "" {
		return errors.New("namespace connot be empty")
	}

	if configFileMetadata.GetFileGroup() == "" {
		return errors.New("fileGroup cannot be empty")
	}

	if configFileMetadata.GetFileName() == "" {
		return errors.New("fileName cannot be empty")
	}

	return nil
}
