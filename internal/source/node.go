package source

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/prom"
)

var chipDevice = regexp.MustCompile(`([0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2})_([0-9a-fA-F])$`)

// ChipPCI is the PCI device of an hwmon chip label: its last 0000:xx:yy_z part
// with the underscore written back as a dot. "" for a chip that is not a PCI
// device (coretemp).
func ChipPCI(chip string) string {
	m := chipDevice.FindStringSubmatch(chip)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1]) + "." + m[2]
}

var gpuDrivers = map[string]bool{"amdgpu": true, "xe": true, "i915": true}

// ParseNode reads a node_exporter scrape (contract section 3).
func ParseNode(s *prom.Set, at time.Time) model.Node {
	n := model.Node{At: at}
	for _, smp := range s.Samples("node_cpu_seconds_total") {
		n.CPUTotal += smp.Value
		if smp.Labels["mode"] == "idle" {
			n.CPUIdle += smp.Value
		}
	}
	if v, ok := s.Value("node_memory_MemTotal_bytes"); ok {
		n.MemTotal = model.Some(v)
	}
	if v, ok := s.Value("node_memory_MemAvailable_bytes"); ok {
		n.MemAvail = model.Some(v)
	}
	if v, ok := s.Value("node_load1"); ok {
		n.Load1 = model.Some(v)
	}

	// Sensor labels, per chip and sensor.
	labels := map[[2]string]string{}
	for _, smp := range s.Samples("node_hwmon_sensor_label") {
		labels[[2]string{smp.Labels["chip"], smp.Labels["sensor"]}] = strings.ToLower(smp.Labels["label"])
	}
	byChip := func(name string) map[string][]prom.Sample {
		m := map[string][]prom.Sample{}
		for _, smp := range s.Samples(name) {
			m[smp.Labels["chip"]] = append(m[smp.Labels["chip"]], smp)
		}
		return m
	}
	temps, powerAvg, power := byChip("node_hwmon_temp_celsius"), byChip("node_hwmon_power_average_watt"), byChip("node_hwmon_power_watt")
	energy, caps, fans, freqs := byChip("node_hwmon_energy_joule_total"), byChip("node_hwmon_power_cap_watt"), byChip("node_hwmon_fan_rpm"), byChip("node_hwmon_freq_freq_mhz")

	for _, smp := range s.Samples("node_hwmon_chip_names") {
		driver := smp.Labels["chip_name"]
		if !gpuDrivers[driver] {
			continue
		}
		chip := smp.Labels["chip"]
		g := model.GPU{Chip: chip, PCI: ChipPCI(chip), Driver: driver, Temps: map[string]float64{}}
		for _, t := range temps[chip] {
			label := labels[[2]string{chip, t.Labels["sensor"]}]
			if label == "" {
				label = t.Labels["sensor"]
			}
			g.Temps[label] = t.Value
		}
		for _, src := range [][]prom.Sample{powerAvg[chip], power[chip]} {
			if len(src) > 0 {
				g.Power = model.Some(src[0].Value)
				break
			}
		}
		for _, e := range energy[chip] { // prefer the package counter
			if !g.Energy.OK || labels[[2]string{chip, e.Labels["sensor"]}] == "pkg" {
				g.Energy = model.Some(e.Value)
			}
		}
		if c := caps[chip]; len(c) > 0 {
			g.Cap = model.Some(c[0].Value)
		}
		for _, f := range fans[chip] {
			if f.Labels["sensor"] == "fan1" {
				g.Fan = model.Some(f.Value)
			}
		}
		for _, f := range freqs[chip] {
			sensor := f.Labels["sensor"]
			if sensor == "sclk" || labels[[2]string{chip, sensor}] == "sclk" {
				g.Clock = model.Some(f.Value)
			}
		}
		n.GPUs = append(n.GPUs, g)
	}

	n.Cards = parseCards(s)
	joinCards(&n)
	return n
}

func cardNumber(name string) int {
	v, err := strconv.Atoi(strings.TrimPrefix(name, "card"))
	if err != nil {
		return 1 << 30
	}
	return v
}

func parseCards(s *prom.Set) []model.DRMCard {
	cards := map[string]*model.DRMCard{}
	get := func(name string) *model.DRMCard {
		if cards[name] == nil {
			cards[name] = &model.DRMCard{Name: name}
		}
		return cards[name]
	}
	each := func(metric string, set func(c *model.DRMCard, v float64)) {
		for _, smp := range s.Samples(metric) {
			if name := smp.Labels["card"]; name != "" {
				set(get(name), smp.Value)
			}
		}
	}
	each("node_drm_gpu_busy_percent", func(c *model.DRMCard, v float64) { c.Busy = model.Some(v) })
	each("node_drm_memory_vram_size_bytes", func(c *model.DRMCard, v float64) { c.VRAMSize = model.Some(v) })
	each("node_drm_memory_vram_used_bytes", func(c *model.DRMCard, v float64) { c.VRAMUsed = model.Some(v) })
	each("node_drm_memory_gtt_size_bytes", func(c *model.DRMCard, v float64) { c.GTTSize = model.Some(v) })
	each("node_drm_memory_gtt_used_bytes", func(c *model.DRMCard, v float64) { c.GTTUsed = model.Some(v) })
	for _, smp := range s.Samples("drm_card_device_info") {
		if name := smp.Labels["card"]; name != "" {
			get(name).Device = strings.ToLower(smp.Labels["device"])
		}
	}
	out := make([]model.DRMCard, 0, len(cards))
	for _, c := range cards {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return cardNumber(out[i].Name) < cardNumber(out[j].Name) })
	return out
}

// joinCards sets GPU.Card. With drm_card_device_info the join is by PCI device.
// Without that metric the amdgpu chips (by PCI address) are paired with the
// cards that carry drm data (by card number) in order, when the counts match;
// otherwise no GPU gets busy or VRAM. The drm collector is amdgpu-only, so the
// cards with data are the amdgpu ones.
func joinCards(n *model.Node) {
	haveInfo := false
	for _, c := range n.Cards {
		if c.Device != "" {
			haveInfo = true
		}
	}
	if haveInfo {
		for i := range n.GPUs {
			for _, c := range n.Cards {
				if c.Device != "" && c.Device == n.GPUs[i].PCI {
					n.GPUs[i].Card = c.Name
				}
			}
		}
		return
	}
	var amd []int
	for i, g := range n.GPUs {
		if g.Driver == "amdgpu" {
			amd = append(amd, i)
		}
	}
	var cards []model.DRMCard
	for _, c := range n.Cards {
		if c.Busy.OK || c.VRAMSize.OK {
			cards = append(cards, c)
		}
	}
	if len(amd) == 0 || len(amd) != len(cards) {
		return
	}
	sort.Slice(amd, func(a, b int) bool { return n.GPUs[amd[a]].PCI < n.GPUs[amd[b]].PCI })
	for k, i := range amd {
		n.GPUs[i].Card = cards[k].Name
	}
}
