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
//
//@Author: springliao
//@Description:
//@Time: 2021/11/11 17:09

package utils

import (
	"reflect"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
)

func Test_getLocationInTencentCloud(t *testing.T) {
	tests := []struct {
		name string
		want *model.Location
	}{
		{
			name: "Test_1",
			want: &model.Location{
				Region: "ap-guangzhou",
				Zone:   "ap-guangzhou-1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getLocationInTencentCloud(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getLocationInTencentCloud() = %v, want %v", got, tt.want)
			}
		})
	}
}
