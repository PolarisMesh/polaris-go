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

package serviceroute

import (
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/plugin/statreporter/basereporter"
)

// Config 插件配置
type Config struct {
	ReportInterval *time.Duration `yaml:"reportInterval" json:"reportInterval"`
}

const (
	defaultReportInterval = 5 * time.Minute
)

// Verify 校验配置
func (c *Config) Verify() error {
	if c.ReportInterval == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"reportInterval of statReporter serviceRoute not configured")
	}
	if *c.ReportInterval < basereporter.MinReportInterval {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"invalid reportInterval %v for statReporter serviceRoute, which must be greater than or equal to %v",
			*c.ReportInterval, basereporter.MinReportInterval)
	}
	return nil
}

// SetDefault 设置默认值
func (c *Config) SetDefault() {
	if c.ReportInterval == nil {
		c.ReportInterval = model.ToDurationPtr(defaultReportInterval)
	}
}