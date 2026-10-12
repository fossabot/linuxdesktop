package core

import (
	"context"
	"testing"

	"github.com/gostafa/linuxdesktop/internal/adapter/desktop"
	"github.com/gostafa/linuxdesktop/internal/domain"
)

func TestCompositorKeepsSupportingDesktopPrivate(t *testing.T) {
	t.Parallel()
	d := Deps{
		Desktop: desktop.Probe(func(context.Context, *domain.Env) (domain.DesktopInfo, error) {
			return domain.DesktopInfo{Environment: domain.DesktopGNOME, Name: "GNOME"}, nil
		}),
	}
	out, err := New(&d, &Config{Sections: domain.SectionCompositor}).Detect(t.Context())
	if err != nil || out.Compositor.Kind != domain.CompositorMutter {
		t.Fatalf("compositor lost supporting evidence: %+v, %v", out, err)
	}
	if out.Desktop.Name != "" || out.Desktop.Environment != "" {
		t.Fatalf("unselected desktop populated: %+v", out.Desktop)
	}
	out, err = New(&d, &Config{Sections: domain.SectionDesktop}).Detect(t.Context())
	if err != nil || out.Desktop.Name != "GNOME" {
		t.Fatal(out, err)
	}
}
