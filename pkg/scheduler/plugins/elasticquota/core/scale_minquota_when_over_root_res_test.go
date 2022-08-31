/*
Copyright 2022 The Koordinator Authors.

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

package core

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

func TestScaleMinQuotaWhenOverRootResInfo_GetScaledMinQuota(t *testing.T) {
	info := NewScaleMinQuotaWhenOverRootResManager()
	if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 0 ||
		len(info.parentToDisableScaleSubsSumMinQuotaMap) != 0 || len(info.originalMinQuotaMap) != 0 || len(info.quotaEnableMinQuotaScaleMap) != 0 {
		t.Error("error")
	}
	{
		parQuotaName := "100"
		subQuotaName := "1"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := false
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)
	}
	{
		parQuotaName := "100"
		subQuotaName := "2"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := true
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)
	}
	{
		parQuotaName := "100"
		subQuotaName := "3"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := true
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)
	}

	{
		totalResource := createResourceList(200, 200)
		result, newMinQuota := info.GetScaledMinQuota(totalResource, "101", "1")
		if result != false || newMinQuota != nil {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "101", "11")
		if result != false || newMinQuota != nil {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "100", "1")
		if result != false || newMinQuota != nil {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "100", "2")
		if result != true || !quotav1.Equals(newMinQuota, createResourceList(50, 50)) {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(util.NewZeroResourceList(), "100", "2")
		if result != true || !quotav1.Equals(newMinQuota, createResourceList(0, 0)) {
			t.Error("error")
		}
	}

	{
		totalResource := createResourceList(100, 100)
		result, newMinQuota := info.GetScaledMinQuota(totalResource, "100", "1")
		if result != false || newMinQuota != nil {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "100", "2")
		if result != true || !quotav1.Equals(newMinQuota, createResourceList(25, 25)) {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "100", "3")
		if result != true || !quotav1.Equals(newMinQuota, createResourceList(25, 25)) {
			t.Error("error")
		}
	}
	{
		totalResource := createResourceList(50, 50)
		result, newMinQuota := info.GetScaledMinQuota(totalResource, "100", "1")
		if result != false || newMinQuota != nil {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "100", "2")
		if result != true || !quotav1.Equals(newMinQuota, createResourceList(0, 0)) {
			t.Error("error")
		}
		result, newMinQuota = info.GetScaledMinQuota(totalResource, "100", "3")
		if result != true || !quotav1.Equals(newMinQuota, createResourceList(0, 0)) {
			t.Error("error")
		}
	}
}

func TestScaleMinQuotaWhenOverRootResInfo_UpdateAndDelete(t *testing.T) {
	info := NewScaleMinQuotaWhenOverRootResManager()
	if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 0 ||
		len(info.parentToDisableScaleSubsSumMinQuotaMap) != 0 || len(info.originalMinQuotaMap) != 0 || len(info.quotaEnableMinQuotaScaleMap) != 0 {
		t.Errorf("error")
	}
	{
		parQuotaName := "100"
		subQuotaName := "1"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := false
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)

		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 1 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 1 || len(info.originalMinQuotaMap) != 1 || len(info.quotaEnableMinQuotaScaleMap) != 1 {
			t.Errorf("error")
		}
		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], v1.ResourceList{}) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != false {
			t.Error("error")
		}
	}
	{
		parQuotaName := "100"
		subQuotaName := "1"
		subMinQuota := createResourceList(40, 40)
		enableScaleMinQuota := true
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)

		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 1 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 1 || len(info.originalMinQuotaMap) != 1 || len(info.quotaEnableMinQuotaScaleMap) != 1 {
			t.Errorf("error")
		}

		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(40, 40)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(40, 40)) || info.quotaEnableMinQuotaScaleMap["1"] != true {
			t.Error("error")
		}
	}
	{
		parQuotaName := "100"
		subQuotaName := "1"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := true
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)

		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 1 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 1 || len(info.originalMinQuotaMap) != 1 || len(info.quotaEnableMinQuotaScaleMap) != 1 {
			t.Errorf("error")
		}

		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true {
			t.Error("error")
		}
	}
	{
		parQuotaName := "100"
		subQuotaName := "2"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := true
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)

		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 1 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 1 || len(info.originalMinQuotaMap) != 2 || len(info.quotaEnableMinQuotaScaleMap) != 2 {
			t.Errorf("error")
		}

		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(100, 100)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["2"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["2"] != true {
			t.Error("error")
		}
	}
	{
		parQuotaName := "100"
		subQuotaName := "3"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := false
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)

		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 1 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 1 || len(info.originalMinQuotaMap) != 3 || len(info.quotaEnableMinQuotaScaleMap) != 3 {
			t.Errorf("error")
		}

		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(100, 100)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["2"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["2"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["3"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["3"] != false {
			t.Error("error")
		}
	}
	{
		parQuotaName := "101"
		subQuotaName := "4"
		subMinQuota := createResourceList(50, 50)
		enableScaleMinQuota := false
		info.Update(parQuotaName, subQuotaName, subMinQuota, enableScaleMinQuota)

		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 2 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 2 || len(info.originalMinQuotaMap) != 4 || len(info.quotaEnableMinQuotaScaleMap) != 4 {
			t.Errorf("error")
		}

		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(100, 100)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["101"], v1.ResourceList{}) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["101"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["2"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["2"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["3"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["3"] != false ||
			!quotav1.Equals(info.originalMinQuotaMap["4"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["4"] != false {
			t.Error("error")
		}
	}

	//begin delete
	{
		parQuotaName := "101"
		subQuotaName := "4"
		info.Delete(parQuotaName, subQuotaName)
		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 2 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 2 || len(info.originalMinQuotaMap) != 3 || len(info.quotaEnableMinQuotaScaleMap) != 3 {
			t.Errorf("error")
		}
		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(100, 100)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["101"], v1.ResourceList{}) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["101"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["2"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["2"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["3"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["3"] != false {
			t.Error("error")
		}
	}
	//begin delete
	{
		parQuotaName := "100"
		subQuotaName := "3"
		info.Delete(parQuotaName, subQuotaName)
		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 2 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 2 || len(info.originalMinQuotaMap) != 2 || len(info.quotaEnableMinQuotaScaleMap) != 2 {
			t.Errorf("error")
		}
		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(100, 100)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["101"], v1.ResourceList{}) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["101"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true ||
			!quotav1.Equals(info.originalMinQuotaMap["2"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["2"] != true {
			t.Error("error")
		}
	}
	//begin delete
	{
		parQuotaName := "100"
		subQuotaName := "2"
		info.Delete(parQuotaName, subQuotaName)
		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 2 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 2 || len(info.originalMinQuotaMap) != 1 || len(info.quotaEnableMinQuotaScaleMap) != 1 {
			t.Errorf("error")
		}
		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(50, 50)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["101"], v1.ResourceList{}) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["101"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.originalMinQuotaMap["1"], createResourceList(50, 50)) || info.quotaEnableMinQuotaScaleMap["1"] != true {
			t.Error("error")
		}
	}
	//begin delete
	{
		parQuotaName := "100"
		subQuotaName := "1"
		info.Delete(parQuotaName, subQuotaName)
		if len(info.parentToEnableScaleSubsSumMinQuotaMap) != 2 ||
			len(info.parentToDisableScaleSubsSumMinQuotaMap) != 2 || len(info.originalMinQuotaMap) != 0 || len(info.quotaEnableMinQuotaScaleMap) != 0 {
			t.Errorf("error")
		}
		if !quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["100"], createResourceList(0, 0)) ||
			!quotav1.Equals(info.parentToEnableScaleSubsSumMinQuotaMap["101"], v1.ResourceList{}) ||
			!quotav1.Equals(info.parentToDisableScaleSubsSumMinQuotaMap["101"], createResourceList(0, 0)) {
			t.Error("error")
		}
	}
}
