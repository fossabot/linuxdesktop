// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package drm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/linuxdesktop/internal/sysfs"
)

// New returns a DRM probe.
func New() Probe {
	return func(ctx context.Context) ([]domain.GPUInfo, string, error) { return gpus(ctx, "") }
}

// GPUs enumerates the DRM cards and returns them along with the id of the
// primary one, which is the card the firmware posted. A machine with no DRM
// devices is not an error: that is what a headless server looks like.
func gpus(
	_ context.Context,
	files string,
) (devices []domain.GPUInfo, primaryID string, failure error) {
	entries, err := sysfs.DirNames(sysfs.Path(files, classDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, noValue, nil
	}

	if err != nil {
		return nil, noValue, fmt.Errorf("drm: enumerate cards: %w", err)
	}

	slices.Sort(entries)

	gpus, primary := collect(files, entries)

	err = resolveModels(files, gpus)

	return gpus, orFirst(primary, gpus), err
}

// newPCIScan prepares a scan for the names the NVIDIA listing did not supply.
func newPCIScan(pending map[string]map[string][]int, gpus []domain.GPUInfo) *pciScan {
	return &pciScan{
		pending:  pending,
		setModel: func(index int, model string) { gpus[index].Model = model },
		left:     countPending(pending),
		devices:  nil,
	}
}

// device handles one indented device line under the vendor currently selected,
// reporting true when nothing is left to name.
func device(scan *pciScan, entry []byte) bool {
	if scan.devices == nil || len(entry) < idFieldWidth || entry[zero] == indentByte {
		return false // no interest in this vendor, or a subsystem line
	}

	id := string(entry[:idFieldWidth])

	indices, ok := scan.devices[id]
	if !ok {
		return false
	}

	name(scan, indices, strings.TrimSpace(string(entry[idFieldWidth:])))
	delete(scan.devices, id)

	return scan.left <= zero
}

// line handles one line of the listing, reporting true when nothing is left to
// name. A line flush against the margin selects a vendor; an indented one
// names a device under it.
func line(scan *pciScan, line []byte) bool {
	if len(line) < idFieldWidth || line[zero] == commentByte {
		return false
	}

	if line[zero] != indentByte {
		scan.devices = scan.pending[string(line[:idFieldWidth])]

		return false
	}

	return device(scan, line[indentWidth:])
}

// name assigns a model to every GPU that was waiting on one device id.
func name(scan *pciScan, indices []int, model string) {
	for i := range indices {
		scan.setModel(indices[i], model)
	}

	scan.left--
}

// run reads the listing, or as much of it as it takes to name every GPU that is
// still waiting.
func run(scan *pciScan, file io.Reader) error {
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		if line(scan, lines.Bytes()) {
			return nil
		}
	}

	callErr := lines.Err()
	if callErr != nil {
		return fmt.Errorf("drm: scan PCI identifiers: %w", callErr)
	}

	return nil
}

// collect builds one GPUInfo per DRM card and names the one the firmware
// posted, which is the only card that carries a boot_vga marker.
func collect(files string, entries []string) (gpus []domain.GPUInfo, primary string) {
	nodes := renderNodesByAddress(files, entries)

	gpus = slices.Grow([]domain.GPUInfo(nil), expectedGPUs)
	primary = noValue

	found := cards(entries)
	for i := range found {
		gpu, boot := describe(files, found[i], nodes)

		gpus = append(gpus, gpu)

		if boot {
			primary = gpu.ID
		}
	}

	return gpus, primary
}

// cards filters the DRM class directory down to the card devices, leaving out
// the connectors and render nodes that share it.
func cards(entries []string) []string {
	found := make([]string, zero, len(entries))

	for i := range entries {
		if isCard(entries[i]) {
			found = append(found, entries[i])
		}
	}

	return found
}

// describe reads everything sysfs knows about one card, and reports whether the
// firmware posted it.
func describe(files, entry string, nodes map[string]string) (domain.GPUInfo, bool) {
	base := filepath.Join(classDir, entry)
	gpu := domain.GPUInfo{
		ID:         entry,
		VendorID:   sysfs.Trimmed(sysfs.Path(files, filepath.Join(base, attrVendor))),
		DeviceID:   sysfs.Trimmed(sysfs.Path(files, filepath.Join(base, attrDevice))),
		PCIAddress: sysfs.LinkBase(sysfs.Path(files, filepath.Join(base, linkDevice))),
		DRMDevice:  filepath.Join(deviceDir, entry),
		Vendor:     noValue, Model: noValue, Driver: noValue, RenderDevice: noValue,
		Integrated: false, Discrete: false,
	}

	applyUevent(files, &gpu, base)

	gpu.RenderDevice = nodes[gpu.PCIAddress]
	gpu.Vendor = vendorNames(gpu.VendorID)

	classify(&gpu)

	return gpu, sysfs.Trimmed(sysfs.Path(files, filepath.Join(base, attrBootVGA))) == "1"
}

// applyUevent overlays the driver name and the PCI slot the kernel reports,
// which is more reliable than the device symlink when both are present.
func applyUevent(files string, gpu *domain.GPUInfo, base string) {
	data, err := sysfs.Bytes(sysfs.Path(files, filepath.Join(base, attrUevent)))
	if err != nil {
		return
	}

	gpu.Driver = sysfs.Field(data, keyDriver)

	if slot := sysfs.Field(data, keySlot); slot != noValue {
		gpu.PCIAddress = slot
	}
}

// orFirst falls back to the first card when the firmware left no boot_vga
// marker, since a machine with a GPU at all has a primary one.
func orFirst(primary string, gpus []domain.GPUInfo) string {
	if primary != noValue || len(gpus) == zero {
		return primary
	}

	return gpus[zero].ID
}

// classify decides whether a GPU is integrated or discrete. Both flags stay
// false when the device is not on a PCI bus at all, which is the honest answer
// for an SoC display engine.
func classify(gpu *domain.GPUInfo) {
	if gpu.PCIAddress == noValue || !strings.Contains(gpu.PCIAddress, ":") {
		gpu.Integrated = true

		return
	}

	if alwaysDiscrete(gpu.VendorID) {
		gpu.Discrete = true

		return
	}

	if strings.HasPrefix(gpu.PCIAddress, rootBusPrefix) {
		gpu.Integrated = true

		return
	}

	gpu.Discrete = true
}

// renderNodesByAddress maps a PCI address to its /dev/dri/renderD* path, by
// resolving each render node's device link to the same device the card points
// at.
func renderNodesByAddress(files string, entries []string) map[string]string {
	nodes := make(map[string]string, expectedGPUs)

	for i := range entries {
		if !strings.HasPrefix(entries[i], renderPrefix) {
			continue
		}

		address := sysfs.LinkBase(
			sysfs.Path(files, filepath.Join(classDir, entries[i], linkDevice)),
		)
		if address == noValue {
			continue
		}

		nodes[address] = filepath.Join(deviceDir, entries[i])
	}

	return nodes
}

// resolveModels fills in Model for every GPU, preferring NVIDIA's own listing
// and otherwise streaming pci.ids exactly once.
func resolveModels(files string, gpus []domain.GPUInfo) error {
	pending := pendingModels(files, gpus)
	if len(pending) == zero {
		return nil
	}

	file := openPCIIDs(files)
	if file == nil {
		return nil
	}

	scan := newPCIScan(pending, gpus)

	return errors.Join(run(scan, file), file.Close())
}

// pendingModels names every GPU it can from NVIDIA's own listing and returns
// the pci.ids lookups still wanted, keyed by vendor and then by device id.
func pendingModels(files string, gpus []domain.GPUInfo) map[string]map[string][]int {
	pending := make(map[string]map[string][]int, expectedGPUs)

	for i := range gpus {
		vendor, device, ok := modelLookup(files, &gpus[i])
		if !ok {
			continue
		}

		if pending[vendor] == nil {
			pending[vendor] = make(map[string][]int)
		}

		pending[vendor][device] = append(pending[vendor][device], i)
	}

	return pending
}

// modelLookup settles where a GPU's model comes from. NVIDIA's proprietary
// driver publishes a marketing name, which wins outright; otherwise the device
// id stands in until pci.ids supplies a real one.
func modelLookup(files string, gpu *domain.GPUInfo) (vendor, device string, ok bool) {
	model := nvidiaModelFor(files, gpu)
	if model != noValue {
		gpu.Model = model

		return noValue, noValue, false
	}

	vendor, device = trimHex(gpu.VendorID), trimHex(gpu.DeviceID)
	if vendor == noValue || device == noValue {
		return noValue, noValue, false
	}

	gpu.Model = gpu.DeviceID // replaced by the scan if pci.ids has a name

	return vendor, device, true
}

// countPending is how many names the scan is looking for in total.
func countPending(pending map[string]map[string][]int) int {
	left := zero

	for vendor := range pending {
		left += len(pending[vendor])
	}

	return left
}

// nvidiaModelFor reads the marketing name the proprietary driver publishes.
func nvidiaModelFor(files string, gpu *domain.GPUInfo) string {
	if !nvidia(gpu) {
		return noValue
	}

	data, err := sysfs.Bytes(
		sysfs.Path(files, filepath.Join(nvidiaGPUDir, gpu.PCIAddress, nvidiaInfo)),
	)
	if err != nil {
		return noValue
	}

	return modelLine(data)
}

// nvidia reports whether the proprietary driver could have published a name for
// this card.
func nvidia(gpu *domain.GPUInfo) bool {
	theirs := gpu.Driver == driverNvidia || gpu.VendorID == vendorIDNvidia

	return theirs && gpu.PCIAddress != noValue
}

// modelLine finds the Model: entry in a /proc/driver/nvidia information file.
func modelLine(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, found := strings.CutPrefix(line, nvidiaModel); found {
			return strings.TrimSpace(rest)
		}
	}

	return noValue
}

func openPCIIDs(files string) *os.File {
	pciIDsPaths := pciIDsPaths()

	for i := range pciIDsPaths {
		file, err := os.Open(sysfs.Path(files, pciIDsPaths[i]))
		if err == nil {
			return file
		}
	}

	return nil
}

func isCard(name string) bool {
	if !strings.HasPrefix(name, cardPrefix) {
		return false
	}

	// card0 is a device; card0-HDMI-A-1 is a connector.
	return !strings.ContainsRune(name[len(cardPrefix):], '-')
}

// trimHex converts a sysfs "0x8086" into the bare "8086" that pci.ids uses.
func trimHex(raw string) string {
	return strings.TrimPrefix(raw, "0x")
}

// GPUs delegates to the configured function.
func (run Func[T]) GPUs(ctx context.Context) (result T, primary string, err error) {
	result, primary, err = run(ctx)
	if err != nil {
		return result, primary, fmt.Errorf("probe: GPUs: %w", err)
	}

	return result, primary, nil
}
