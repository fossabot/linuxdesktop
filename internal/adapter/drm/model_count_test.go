package drm

import (
	"strings"
	"testing"

	"github.com/gostafa/linuxdesktop/internal/domain"
)

func TestDuplicateGPUsDoNotEndModelScan(t *testing.T) {
	t.Parallel()
	gpus := make([]domain.GPUInfo, 3)
	scan := newPCIScan(map[string]map[string][]int{"8086": {"1234": {0, 1}, "5678": {2}}}, gpus)
	if err := run(
		scan,
		strings.NewReader("8086 Intel\n\t1234 First\n\t5678 Second\n"),
	); err != nil {
		t.Fatal(err)
	}
	if gpus[0].Model != "First" || gpus[1].Model != "First" || gpus[2].Model != "Second" {
		t.Fatalf("models incomplete: %+v", gpus)
	}
}
