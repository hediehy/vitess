// Copyright 2015, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package estimator

import (
	"sync"
	"testing"

	"github.com/youtube/vitess/go/ewma"
)

func TestEstimator(t *testing.T) {
	wg := &sync.WaitGroup{}
	e := NewEstimator(1000, 0.8)
	input := map[string][]float64{
		"select aa from t_test where col1=:bv1":              []float64{200000, 210000, 201000, 197000},
		"select bb from t_test where col1=:bv1 or col2=:bv2": []float64{900000, 1100000, 950000, 970000, 990000},
		"select * from t_test_small":                         []float64{10000, 11000, 9000},
	}
	output := map[string]float64{
		"select aa from t_test where col1=:bv1":              200840,
		"select bb from t_test where col1=:bv1 or col2=:bv2": 956080,
		"select * from t_test_small":                         9960,
	}
	// Record history
	for k, v := range input {
		wg.Add(1)
		go func(key string, values []float64) {
			defer wg.Done()
			for _, data := range values {
				e.AddHistory(key, data)
			}
		}(k, v)
	}
	wg.Wait()
	// Validata calculation
	for k, v := range output {
		if ev := e.Estimate(k); ev != v {
			t.Errorf("Expect the estimated value of key %v to be %v, but got %v", k, v, ev)
		}
	}
	if v := e.Estimate("select cc from t_test where col3:=bv1"); v != 0 {
		t.Errorf("Expect estimator to return 0 for new query, but got %v", v)
	}
	// Test invalid arguments to NewEstimator
	e = NewEstimator(0, 0.8)
	if ca := e.records.Capacity(); ca != DefaultCapacity {
		t.Errorf("Expect Estimator to have default capacity(%v), but got %v", DefaultCapacity, ca)
	}
	e = NewEstimator(10, -0.1)
	if e.weightingFactor != ewma.DefaultWeightingFactor {
		t.Errorf("Expect Estimator to have default weighting factor(%v), but got %v", ewma.DefaultWeightingFactor, e.weightingFactor)
	}
}
