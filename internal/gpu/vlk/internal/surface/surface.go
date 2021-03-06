package surface

import (
	"fmt"
	"log"

	"github.com/vulkan-go/vulkan"

	"github.com/go-glx/vgl/arch"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/instance"
)

type Surface struct {
	inst *instance.Instance

	ref vulkan.Surface
}

func NewSurface(inst *instance.Instance, wm arch.WindowManager) *Surface {
	surface, err := wm.CreateSurface(inst.Ref())
	if err != nil {
		panic(fmt.Errorf("failed create vulkan surface: %w", err))
	}

	return &Surface{
		inst: inst,
		ref:  surface,
	}
}

func (s *Surface) Free() {
	vulkan.DestroySurface(s.inst.Ref(), s.ref, nil)
	log.Printf("vk: freed: surface\n")
}

func (s *Surface) Ref() vulkan.Surface {
	return s.ref
}
