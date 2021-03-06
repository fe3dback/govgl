package pipeline

import (
	"github.com/vulkan-go/vulkan"

	"github.com/go-glx/vgl/internal/gpu/vlk/internal/logical"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/must"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/renderpass"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/swapchain"
)

type Factory struct {
	ld             *logical.Device
	swapChain      *swapchain.Chain
	mainRenderPass *renderpass.Pass

	defaultPipelineLayout vulkan.PipelineLayout
	createdPipelines      []vulkan.Pipeline
}

func NewFactory(ld *logical.Device, swapChain *swapchain.Chain, mainRenderPass *renderpass.Pass) *Factory {
	factory := &Factory{
		ld:             ld,
		swapChain:      swapChain,
		mainRenderPass: mainRenderPass,
	}

	factory.defaultPipelineLayout = factory.newDefaultPipelineLayout()
	return factory
}

func (f *Factory) Free() {
	vulkan.DestroyPipelineLayout(f.ld.Ref(), f.defaultPipelineLayout, nil)

	for _, pipeline := range f.createdPipelines {
		vulkan.DestroyPipeline(f.ld.Ref(), pipeline, nil)
	}
}

func (f *Factory) NewPipeline(opts ...Initializer) vulkan.Pipeline {
	info := vulkan.GraphicsPipelineCreateInfo{
		SType: vulkan.StructureTypeGraphicsPipelineCreateInfo,
	}

	// default opts
	opts = append(opts, f.withDefaultViewport())
	opts = append(opts, f.withDefaultMainRenderPass())
	opts = append(opts, f.withDefaultLayout())

	// build pipeline info
	for _, applyOpt := range opts {
		applyOpt(&info)
	}

	// create pipeline from it
	pipelines := make([]vulkan.Pipeline, 1)
	result := vulkan.CreateGraphicsPipelines(
		f.ld.Ref(),
		nil, // todo: cache
		1,
		[]vulkan.GraphicsPipelineCreateInfo{info},
		nil,
		pipelines,
	)

	must.Work(result)

	pipeline := pipelines[0]
	f.createdPipelines = append(f.createdPipelines, pipeline)
	return pipeline
}
