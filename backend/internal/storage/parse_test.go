package storage

import "testing"

// Shaped like real `lsblk -J -b -O` output, including the loop devices a box
// running snaps accumulates and the null fields lsblk emits for unformatted
// space.
const lsblkFixture = `{
  "blockdevices": [
    {"name":"loop0","path":"/dev/loop0","type":"loop","size":74027008,"rota":false,
     "model":null,"serial":null,"mountpoint":"/snap/core22/1122"},
    {"name":"sda","path":"/dev/sda","type":"disk","size":4000787030016,"rota":true,
     "rm":false,"tran":"sata","model":"WDC WD40EFRX-68N32N0","serial":"WD-WCC7K0000001",
     "vendor":"ATA     ","fstype":null,"mountpoint":null,
     "children":[
       {"name":"sda1","path":"/dev/sda1","type":"part","size":4000784957440,
        "fstype":"ext4","label":"media","uuid":"3f2b-aa11","mountpoint":"/mnt/storage/media",
        "fsused":1200000000000,"fsavail":2600000000000}
     ]},
    {"name":"nvme0n1","path":"/dev/nvme0n1","type":"disk","size":512110190592,"rota":false,
     "rm":false,"tran":"nvme","model":"Samsung SSD 980 500GB","serial":"S64ANF0R000001",
     "children":[
       {"name":"nvme0n1p1","path":"/dev/nvme0n1p1","type":"part","size":536870912,
        "fstype":"vfat","label":"EFI","mountpoint":"/boot/efi","fsused":12000000,"fsavail":524000000},
       {"name":"nvme0n1p2","path":"/dev/nvme0n1p2","type":"part","size":511571427328,
        "fstype":"ext4","label":null,"mountpoint":"/","fsused":40000000000,"fsavail":430000000000}
     ]},
    {"name":"sdb","path":"/dev/sdb","type":"disk","size":1000204886016,"rota":true,
     "rm":false,"tran":"usb","model":"Elements 25A3","serial":"575836314441",
     "children":[]}
  ]
}`

func TestParseLsblk(t *testing.T) {
	devs, err := parseLsblk([]byte(lsblkFixture))
	if err != nil {
		t.Fatalf("parseLsblk: %v", err)
	}
	if len(devs) != 3 {
		t.Fatalf("got %d devices, want 3 (loop must be filtered): %+v", len(devs), devs)
	}

	byName := map[string]Device{}
	for _, d := range devs {
		byName[d.Name] = d
	}

	sda := byName["sda"]
	if sda.SizeBytes != 4000787030016 {
		t.Errorf("sda size = %d", sda.SizeBytes)
	}
	if !sda.Rotational || sda.Transport != "sata" {
		t.Errorf("sda = %+v, want rotational sata", sda)
	}
	if sda.Model != "WDC WD40EFRX-68N32N0" {
		t.Errorf("sda model = %q", sda.Model)
	}
	if !sda.InUse {
		t.Error("sda has a mounted partition and should be marked in use")
	}
	if len(sda.Partitions) != 1 {
		t.Fatalf("sda partitions = %d", len(sda.Partitions))
	}
	p := sda.Partitions[0]
	if p.Mountpoint != "/mnt/storage/media" || p.Label != "media" {
		t.Errorf("sda1 = %+v", p)
	}
	// 1.2TB used of 3.8TB accounted -> ~31.6%
	if p.UsedPercent < 31 || p.UsedPercent > 32 {
		t.Errorf("sda1 used percent = %v, want ~31.6", p.UsedPercent)
	}

	nvme := byName["nvme0n1"]
	if len(nvme.Partitions) != 2 || nvme.Rotational {
		t.Errorf("nvme = %+v", nvme)
	}
	if !nvme.InUse {
		t.Error("nvme is mounted at / and should be in use")
	}

	sdb := byName["sdb"]
	if sdb.InUse {
		t.Error("sdb has no mounted partition and should be free")
	}
	if len(sdb.Partitions) != 0 {
		t.Errorf("sdb partitions = %+v", sdb.Partitions)
	}
}

// util-linux 2.38 renders sizes and booleans as strings; 2.39 uses JSON types.
// Both must parse identically.
func TestParseLsblkToleratesStringTypes(t *testing.T) {
	const oldStyle = `{"blockdevices":[
	  {"name":"sda","path":"/dev/sda","type":"disk","size":"500107862016","rota":"1","rm":"0",
	   "children":[{"name":"sda1","path":"/dev/sda1","type":"part","size":"500106813440",
	                "fstype":"btrfs","mountpoint":"/mnt/storage/tank",
	                "fsused":"100000000000","fsavail":"400000000000"}]}]}`

	devs, err := parseLsblk([]byte(oldStyle))
	if err != nil {
		t.Fatalf("parseLsblk: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("got %d devices", len(devs))
	}
	if devs[0].SizeBytes != 500107862016 {
		t.Errorf("size = %d, want the string value parsed as a number", devs[0].SizeBytes)
	}
	if !devs[0].Rotational {
		t.Error(`rota "1" should parse as true`)
	}
	if devs[0].Removable {
		t.Error(`rm "0" should parse as false`)
	}
	if devs[0].Partitions[0].UsedBytes != 100000000000 {
		t.Errorf("fsused = %d", devs[0].Partitions[0].UsedBytes)
	}
}

func TestParseLsblkRejectsGarbage(t *testing.T) {
	if _, err := parseLsblk([]byte("not json")); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}
