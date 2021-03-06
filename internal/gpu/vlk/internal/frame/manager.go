package frame

import (
	"log"

	"github.com/vulkan-go/vulkan"

	"github.com/go-glx/vgl/internal/gpu/vlk/internal/command"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/def"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/logical"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/must"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/renderpass"
	"github.com/go-glx/vgl/internal/gpu/vlk/internal/swapchain"
)

type Manager struct {
	chain          *swapchain.Chain
	mainRenderPass *renderpass.Pass
	ld             *logical.Device
	onSuboptimal   func()

	available bool
	frameID   uint32
	imageID   uint32
	count     uint32

	semRenderAvailable  map[uint32]vulkan.Semaphore
	semPresentAvailable map[uint32]vulkan.Semaphore
	syncFrameBusy       map[uint32]vulkan.Fence
	syncImageBusy       map[uint32]vulkan.Fence
	commandBuffers      map[uint32]vulkan.CommandBuffer
}

func NewManager(ld *logical.Device, pool *command.Pool, chain *swapchain.Chain, renderToScreenPass *renderpass.Pass, onSuboptimal func()) *Manager {
	m := &Manager{
		chain:          chain,
		mainRenderPass: renderToScreenPass,
		ld:             ld,
		onSuboptimal:   onSuboptimal,

		available: true,
		frameID:   0,
		imageID:   0,
		count:     uint32(pool.BuffersCount()),

		semRenderAvailable:  make(map[uint32]vulkan.Semaphore),
		semPresentAvailable: make(map[uint32]vulkan.Semaphore),
		syncFrameBusy:       make(map[uint32]vulkan.Fence),
		syncImageBusy:       make(map[uint32]vulkan.Fence),
		commandBuffers:      make(map[uint32]vulkan.CommandBuffer),
	}

	for fID := uint32(0); fID < m.count; fID++ {
		m.commandBuffers[fID] = pool.CommandBuffer(int(fID))
		m.semRenderAvailable[fID] = allocateSemaphore(ld)
		m.semPresentAvailable[fID] = allocateSemaphore(ld)
		m.syncFrameBusy[fID] = allocateFence(ld)
	}

	log.Printf("vk: frame manager created\n")
	return m
}

func (m *Manager) Free() {
	for fID := uint32(0); fID < m.count; fID++ {
		vulkan.DestroyFence(m.ld.Ref(), m.syncFrameBusy[fID], nil)
		vulkan.DestroySemaphore(m.ld.Ref(), m.semPresentAvailable[fID], nil)
		vulkan.DestroySemaphore(m.ld.Ref(), m.semRenderAvailable[fID], nil)
	}

	log.Printf("vk: freed: frames manager\n")
}

func (m *Manager) FrameBegin() {
	m.prepareFrame()
	if !m.available {
		m.nextFrame()
		return
	}

	// start buffer
	m.commandBufferBegin()

	// start render pass
	m.FrameApplyCommands(func(imageID uint32, cb vulkan.CommandBuffer) {
		m.renderPassMainBegin(imageID, cb)
	})
}

func (m *Manager) prepareFrame() {
	m.available = true
	timeout := uint64(def.FrameAcquireTimeout.Nanoseconds())
	renderDone := m.syncFrameBusy[m.frameID]

	// wait for rendering in current frame is done
	// then we can occupy current frame for next rendering
	ok := must.NotCare(vulkan.WaitForFences(m.ld.Ref(), 1, []vulkan.Fence{renderDone}, vulkan.True, timeout))
	if !ok {
		m.available = false
		return
	}

	// acquire new image
	m.imageID, m.available = m.acquireNextImage()

	// frame suboptimal, skip rendering
	if !m.available {
		return
	}

	// wait when image will be available
	if imageBusy, inFlight := m.syncImageBusy[m.imageID]; inFlight {
		must.NotCare(vulkan.WaitForFences(m.ld.Ref(), 1, []vulkan.Fence{imageBusy}, vulkan.True, timeout))
	}
	m.syncImageBusy[m.imageID] = renderDone

	// reset render done fence
	vulkan.ResetFences(m.ld.Ref(), 1, []vulkan.Fence{renderDone})
}

func (m *Manager) FrameApplyCommands(apply func(imageID uint32, cb vulkan.CommandBuffer)) {
	if !m.available {
		return
	}

	apply(m.imageID, m.commandBuffers[m.frameID])
}

func (m *Manager) FrameEnd() {
	if !m.available {
		return
	}

	// end render pass
	m.FrameApplyCommands(func(imageID uint32, cb vulkan.CommandBuffer) {
		m.renderPassMainEnd(cb)
	})

	// end buffer
	m.commandBufferEnd()

	// submit rendering on GPU
	m.submit()

	// frame end
	m.nextFrame()
}

func (m *Manager) nextFrame() {
	m.frameID = (m.frameID + 1) % m.count
}

func (m *Manager) submit() {
	if !m.render() {
		return
	}

	if !m.present() {
		return
	}

	vulkan.QueueWaitIdle(m.ld.QueuePresent())
}

func (m *Manager) acquireNextImage() (uint32, bool) {
	timeout := uint64(def.FrameAcquireTimeout.Nanoseconds())
	imageID := uint32(0)

	result := vulkan.AcquireNextImage(m.ld.Ref(), m.chain.Ref(), timeout, m.semRenderAvailable[m.frameID], nil, &imageID)
	if result == vulkan.ErrorOutOfDate || result == vulkan.Suboptimal {
		// buffer size changes (window rebuildGraphicsPipeline, minimize, etc..)
		// and not more valid
		m.onSuboptimal()
		return 0, false
	}

	if result != vulkan.Success {
		must.NotCare(result)
		return 0, false
	}

	return imageID, true
}

func (m *Manager) render() bool {
	info := vulkan.SubmitInfo{
		SType:                vulkan.StructureTypeSubmitInfo,
		WaitSemaphoreCount:   1,
		PWaitSemaphores:      []vulkan.Semaphore{m.semRenderAvailable[m.frameID]},
		PWaitDstStageMask:    []vulkan.PipelineStageFlags{vulkan.PipelineStageFlags(vulkan.PipelineStageColorAttachmentOutputBit)},
		CommandBufferCount:   1,
		PCommandBuffers:      []vulkan.CommandBuffer{m.commandBuffers[m.frameID]},
		SignalSemaphoreCount: 1,
		PSignalSemaphores:    []vulkan.Semaphore{m.semPresentAvailable[m.frameID]},
	}

	return must.NotCare(vulkan.QueueSubmit(m.ld.QueueGraphics(), 1, []vulkan.SubmitInfo{info}, m.syncFrameBusy[m.frameID]))
}

func (m *Manager) present() bool {
	info := &vulkan.PresentInfo{
		SType:              vulkan.StructureTypePresentInfo,
		WaitSemaphoreCount: 1,
		PWaitSemaphores:    []vulkan.Semaphore{m.semPresentAvailable[m.frameID]},
		SwapchainCount:     1,
		PSwapchains:        []vulkan.Swapchain{m.chain.Ref()},
		PImageIndices:      []uint32{m.imageID},
	}

	return must.NotCare(vulkan.QueuePresent(m.ld.QueuePresent(), info))
}
