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

package extension

import (
	"reflect"
	"testing"
)

func TestGetResctrlInfo(t *testing.T) {
	type args struct {
		annotation map[string]string
	}

	tests := []struct {
		name    string
		args    args
		want    *ResctrlConfig
		wantErr bool
	}{
		{
			name: "only MB parse",
			args: args{
				annotation: map[string]string{
					AnnotationResctrl: `{
                    "mb": {
                      "schemata": {
                        "percent": 35
                      },
                      "schemataPerCache": [
                        {
                          "cacheid": 0,
                          "percent": 80
                        }
                      ]
                    }
                  }`,
				},
			},
			want: &ResctrlConfig{
				MB: MB{
					Schemata: SchemataConfig{
						Percent: 35,
						Range:   nil,
					},
					SchemataPerCache: []SchemataPerCacheConfig{{
						CacheID: 0,
						SchemataConfig: SchemataConfig{
							Percent: 80,
							Range:   nil,
						},
					}},
				},
			},
		},
		{
			name: "only LLC parse",
			args: args{
				annotation: map[string]string{
					AnnotationResctrl: `
                    {
                       "llc": {
                         "schemata": {
                           "range": [20,80]
                         },
                         "schemataPerCache": [
                           {
                             "cacheid": 0,
                             "range": [20,30]
                           }
                         ]
                       }
                     }`,
				},
			},
			want: &ResctrlConfig{
				LLC: LLC{
					Schemata: SchemataConfig{
						Percent: 0,
						Range:   []int{20, 80},
					},
					SchemataPerCache: []SchemataPerCacheConfig{{
						CacheID: 0,
						SchemataConfig: SchemataConfig{
							Percent: 0,
							Range:   []int{20, 30},
						},
					}},
				},
			},
		},
		{
			name: "MB and LLC parse",
			args: args{
				annotation: map[string]string{
					AnnotationResctrl: `
                    {
                        "mb": {
                          "schemata": {
                            "percent": 35
                          },
                          "schemataPerCache": [
                            {
                              "cacheid": 0,
                              "percent": 80
                            }
                          ]
                        },
                        "llc": {
                         "schemata": {
                           "range": [20,80]
                         },
                         "schemataPerCache": [
                           {
                             "cacheid": 0,
                             "range": [20,30]
                           }
                         ]
                       }
                    }`,
				},
			},
			want: &ResctrlConfig{
				LLC: LLC{
					Schemata: SchemataConfig{
						Percent: 0,
						Range:   []int{20, 80},
					},
					SchemataPerCache: []SchemataPerCacheConfig{{
						CacheID: 0,
						SchemataConfig: SchemataConfig{
							Percent: 0,
							Range:   []int{20, 30},
						},
					}},
				},
				MB: MB{
					Schemata: SchemataConfig{
						Percent: 35,
						Range:   nil,
					},
					SchemataPerCache: []SchemataPerCacheConfig{{
						CacheID: 0,
						SchemataConfig: SchemataConfig{
							Percent: 80,
							Range:   nil,
						},
					}},
				},
			},
		},
		{
			name: "parse error",
			args: args{
				annotation: map[string]string{
					AnnotationResctrl: `test`,
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetResctrlInfo(tt.args.annotation)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetResctrlInfo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetResctrlInfo() got = %v, want %v", got, tt.want)
			}
		})
	}
}
