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

package cpu

import (
	"fmt"
	"sync/atomic"
	"time"
)

const (
	interval time.Duration = time.Millisecond * 500
)

var (
	stats CPU
	usage uint64
)

// CPU is cpu stat usage.
type CPU interface {
	Usage() (u uint64, e error)
	Info() Info
}

func Init() error {
	var err error
	stats, err = newCgroupCPU()
	if err != nil {
		// fmt.Printf("cgroup cpu init failed(%v),switch to psutil cpu\n", err)
		stats, err = newPsutilCPU(interval)
		if err != nil {
			return fmt.Errorf("cgroup cpu init failed. err: %w", err)
		}
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			<-ticker.C
			u, err := stats.Usage()
			if err == nil && u != 0 {
				atomic.StoreUint64(&usage, u)
			}
		}
	}()
	return nil
}

// Stat cpu stat.
type Stat struct {
	Usage uint64 // cpu use ratio.
}

// Info cpu info.
type Info struct {
	Frequency uint64
	Quota     float64
}

// ReadStat read cpu stat.
func ReadStat(stat *Stat) {
	stat.Usage = atomic.LoadUint64(&usage)
}

// GetInfo get cpu info.
func GetInfo() Info {
	return stats.Info()
}