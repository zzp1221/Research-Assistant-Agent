package agent

import (
	"reflect"
	"testing"
)

func TestPythonModuleCandidatesCommonPackages(t *testing.T) {
	cases := map[string][]string{
		"scikit-learn":      {"sklearn"},
		"pyyaml":            {"yaml"},
		"Pillow":            {"PIL"},
		"opencv-python":     {"cv2"},
		"python-dateutil":   {"dateutil"},
		"beautifulsoup4":    {"bs4"},
		"scikit-learn==1.5": {"sklearn"},
	}
	for name, expectedFirst := range cases {
		got := pythonModuleCandidates(name)
		if len(got) == 0 {
			t.Fatalf("%s: no candidates", name)
		}
		if !reflect.DeepEqual(got[0:len(expectedFirst)], expectedFirst) {
			t.Fatalf("%s: expected %v, got %v", name, expectedFirst, got)
		}
	}
}

func TestPythonModuleCandidatesUnknownFallsBackToUnderscore(t *testing.T) {
	got := pythonModuleCandidates("some-new-package")
	if len(got) == 0 || got[len(got)-1] != "some_new_package" {
		t.Fatalf("unexpected candidates: %v", got)
	}
}
