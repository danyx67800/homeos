package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// lsblkOutput mirrors `lsblk -J -b -O`.
type lsblkOutput struct {
	BlockDevices []lsblkNode `json:"blockdevices"`
}

type lsblkNode struct {
	Name       flexString  `json:"name"`
	Path       flexString  `json:"path"`
	KName      flexString  `json:"kname"`
	Type       flexString  `json:"type"`
	Size       flexUint64  `json:"size"`
	Model      flexString  `json:"model"`
	Serial     flexString  `json:"serial"`
	Vendor     flexString  `json:"vendor"`
	Rota       flexBool    `json:"rota"`
	RM         flexBool    `json:"rm"`
	Tran       flexString  `json:"tran"`
	Fstype     flexString  `json:"fstype"`
	Label      flexString  `json:"label"`
	UUID       flexString  `json:"uuid"`
	Mountpoint flexString  `json:"mountpoint"`
	FSUsed     flexUint64  `json:"fsused"`
	FSAvail    flexUint64  `json:"fsavail"`
	Children   []lsblkNode `json:"children"`
}

// parseLsblk turns lsblk JSON into the device tree the API serves.
//
// Loop, ram and rom devices are filtered out: they are never user storage, and
// on a box running Docker there can be dozens of loop devices from snaps.
func parseLsblk(raw []byte) ([]Device, error) {
	var out lsblkOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse lsblk output: %w", err)
	}

	devices := make([]Device, 0, len(out.BlockDevices))
	for _, n := range out.BlockDevices {
		if !isUserStorage(string(n.Type), string(n.Name)) {
			continue
		}
		devPath := string(n.Path)
		if devPath == "" {
			devPath = "/dev/" + string(n.Name)
		}

		d := Device{
			Path:       devPath,
			Name:       string(n.Name),
			Model:      string(n.Model),
			Serial:     string(n.Serial),
			Vendor:     string(n.Vendor),
			SizeBytes:  uint64(n.Size),
			Rotational: bool(n.Rota),
			Removable:  bool(n.RM),
			Transport:  string(n.Tran),
			Type:       string(n.Type),
			Partitions: make([]Partition, 0, len(n.Children)),
		}

		// A disk with no partition table can itself carry a filesystem and be
		// mounted; that still counts as in use.
		if string(n.Mountpoint) != "" {
			d.InUse = true
		}

		for _, c := range n.Children {
			if string(c.Type) != "part" {
				continue
			}
			pPath := string(c.Path)
			if pPath == "" {
				pPath = "/dev/" + string(c.Name)
			}
			p := Partition{
				Path:       pPath,
				Name:       string(c.Name),
				SizeBytes:  uint64(c.Size),
				Fstype:     string(c.Fstype),
				Label:      string(c.Label),
				UUID:       string(c.UUID),
				Mountpoint: string(c.Mountpoint),
				UsedBytes:  uint64(c.FSUsed),
				FreeBytes:  uint64(c.FSAvail),
			}
			if p.Mountpoint != "" {
				d.InUse = true
			}
			if total := p.UsedBytes + p.FreeBytes; total > 0 {
				p.UsedPercent = round1(float64(p.UsedBytes) / float64(total) * 100)
			}
			d.Partitions = append(d.Partitions, p)
		}

		sort.Slice(d.Partitions, func(i, j int) bool {
			return d.Partitions[i].Name < d.Partitions[j].Name
		})
		devices = append(devices, d)
	}

	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, nil
}

func isUserStorage(typ, name string) bool {
	if typ != "disk" {
		return false
	}
	for _, p := range []string{"loop", "ram", "sr", "zram", "dm-", "md"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
