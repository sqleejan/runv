// +build linux,with_libvirt

package libvirt

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"github.com/hyperhq/runv/hypervisor"
	"github.com/hyperhq/runv/lib/utils"
	libvirtgo "github.com/rgbkrk/libvirt-go"
)

var LibvirtdAddress = "qemu:///system"

type LibvirtDriver struct {
	conn    libvirtgo.VirConnection
	domains map[string]*hypervisor.VmContext
}

type LibvirtContext struct {
	driver *LibvirtDriver
	domain *libvirtgo.VirDomain
}

func InitDriver() *LibvirtDriver {
	/* Libvirt adds memballoon device by default */
	hypervisor.PciAddrFrom = 0x06
	conn, err := libvirtgo.NewVirConnection(LibvirtdAddress)
	if err != nil {
		glog.Error("fail to connect to libvirtd ", LibvirtdAddress, err)
		return nil
	}

	return &LibvirtDriver{
		conn:    conn,
		domains: make(map[string]*hypervisor.VmContext),
	}
}

func (ld *LibvirtDriver) InitContext(homeDir string) hypervisor.DriverContext {
	return &LibvirtContext{
		driver: ld,
	}
}

func (ld *LibvirtDriver) LoadContext(persisted map[string]interface{}) (hypervisor.DriverContext, error) {
	if t, ok := persisted["hypervisor"]; !ok || t != "libvirt" {
		return nil, fmt.Errorf("wrong driver type %v in persist info, expect libvirt", t)
	}

	name, ok := persisted["name"]
	if !ok {
		return nil, fmt.Errorf("there is no libvirt domain name")
	}

	domain, err := ld.conn.LookupDomainByName(name.(string))
	if err != nil {
		return nil, fmt.Errorf("cannot find domain whose name is %v", name)
	}

	return &LibvirtContext{
		driver: ld,
		domain: &domain,
	}, nil
}

func (ld *LibvirtDriver) SupportLazyMode() bool {
	return false
}

type memory struct {
	Unit    string `xml:"unit,attr"`
	Content int    `xml:",chardata"`
}

type maxmem struct {
	Unit    string `xml:"unit,attr"`
	Slots   string `xml:"slots,attr"`
	Content int    `xml:",chardata"`
}

type vcpu struct {
	Placement string `xml:"placement,attr"`
	Current   string `xml:"current,attr"`
	Content   int    `xml:",chardata"`
}

type cpumodel struct {
	Fallback string `xml:"fallback,attr"`
	Content  string `xml:",chardata"`
}

type cell struct {
	Id     string `xml:"id,attr"`
	Cpus   string `xml:"cpus,attr"`
	Memory string `xml:"memory,attr"`
	Unit   string `xml:"unit,attr"`
}

type numa struct {
	Cell []cell `xml:"cell"`
}

type cpu struct {
	Mode  string    `xml:"mode,attr"`
	Match string    `xml:"match,attr,omitempty"`
	Model *cpumodel `xml:"model,omitempty"`
	Numa  *numa     `xml:"numa,omitempty"`
}

type ostype struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Content string `xml:",chardata"`
}

type osloader struct {
	Type     string `xml:"type,attr"`
	ReadOnly string `xml:"readonly,attr"`
	Content  string `xml:",chardata"`
}

type domainos struct {
	Supported string    `xml:"supported,attr"`
	Type      ostype    `xml:"type"`
	Kernel    string    `xml:"kernel,omitempty"`
	Initrd    string    `xml:"initrd,omitempty"`
	Cmdline   string    `xml:"cmdline,omitempty"`
	Loader    *osloader `xml:"loader,omitempty"`
	Nvram     string    `xml:"nvram,omitempty"`
}

type features struct {
	Acpi string `xml:"acpi"`
}

type address struct {
	Type       string `xml:"type,attr"`
	Domain     string `xml:"domain,attr,omitempty"`
	Controller string `xml:"controller,attr,omitempty"`
	Bus        string `xml:"bus,attr"`
	Slot       string `xml:"slot,attr,omitempty"`
	Function   string `xml:"function,attr,omitempty"`
	Target     int    `xml:"target,attr,omitempty"`
	Unit       int    `xml:"unit,attr,omitempty"`
}

type controller struct {
	Type    string   `xml:"type,attr"`
	Index   string   `xml:"index,attr,omitempty"`
	Model   string   `xml:"model,attr,omitempty"`
	Address *address `xml:"address,omitempty"`
}

type fsdriver struct {
	Type string `xml:"type,attr"`
}

type fspath struct {
	Dir string `xml:"dir,attr"`
}

type filesystem struct {
	Type       string   `xml:"type,attr"`
	Accessmode string   `xml:"accessmode,attr"`
	Driver     fsdriver `xml:"driver"`
	Source     fspath   `xml:"source"`
	Target     fspath   `xml:"target"`
	Address    *address `xml:"address"`
}

type channsrc struct {
	Mode string `xml:"mode,attr"`
	Path string `xml:"path,attr"`
}

type channtgt struct {
	Type string `xml:"type,attr"`
	Name string `xml:"name,attr"`
}

type channel struct {
	Type   string   `xml:"type,attr"`
	Source channsrc `xml:"source"`
	Target channtgt `xml:"target"`
}

type constgt struct {
	Type string `xml:"type,attr"`
	Port string `xml:"port,attr"`
}

type console struct {
	Type   string   `xml:"type,attr"`
	Source channsrc `xml:"source"`
	Target constgt  `xml:"target"`
}

type memballoon struct {
	Model   string   `xml:"model,attr"`
	Address *address `xml:"address"`
}

type device struct {
	Emulator    string       `xml:"emulator"`
	Controllers []controller `xml:"controller"`
	Filesystems []filesystem `xml:"filesystem"`
	Channels    []channel    `xml:"channel"`
	Console     console      `xml:"console"`
	Memballoon  memballoon   `xml:"memballoon"`
}

type seclab struct {
	Type string `xml:"type,attr"`
}

type domain struct {
	XMLName    xml.Name `xml:"domain"`
	Type       string   `xml:"type,attr"`
	Name       string   `xml:"name"`
	Memory     memory   `xml:"memory"`
	MaxMem     *maxmem  `xml:"maxMemory,omitempty"`
	VCpu       vcpu     `xml:"vcpu"`
	OS         domainos `xml:"os"`
	Features   features `xml:"features"`
	CPU        cpu      `xml:"cpu"`
	OnPowerOff string   `xml:"on_poweroff"`
	OnReboot   string   `xml:"on_reboot"`
	OnCrash    string   `xml:"on_crash"`
	Devices    device   `xml:"devices"`
	SecLabel   seclab   `xml:"seclabel"`
}

func (lc *LibvirtContext) domainXml(ctx *hypervisor.VmContext) (string, error) {
	if ctx.Boot == nil {
		ctx.Boot = &hypervisor.BootConfig{
			CPU:    1,
			Memory: 128,
			Kernel: hypervisor.DefaultKernel,
			Initrd: hypervisor.DefaultInitrd,
		}
	}
	boot := ctx.Boot

	dom := &domain{
		Type: "kvm",
		Name: ctx.Id,
	}

	dom.Memory.Unit = "MiB"
	dom.Memory.Content = ctx.Boot.Memory

	dom.VCpu.Placement = "static"
	dom.VCpu.Current = strconv.Itoa(ctx.Boot.CPU)
	dom.VCpu.Content = ctx.Boot.CPU

	dom.OS.Supported = "yes"
	dom.OS.Type.Arch = "x86_64"
	dom.OS.Type.Machine = "pc-i440fx-2.0"
	dom.OS.Type.Content = "hvm"

	dom.SecLabel.Type = "none"

	dom.CPU.Mode = "host-passthrough"
	if _, err := os.Stat("/dev/kvm"); os.IsNotExist(err) {
		dom.Type = "qemu"
		dom.CPU.Mode = "host-model"
		dom.CPU.Match = "exact"
		dom.CPU.Model = &cpumodel{
			Fallback: "allow",
			Content:  "core2duo",
		}
	}

	if ctx.Boot.HotAddCpuMem {
		dom.OS.Type.Machine = "pc-i440fx-2.1"
		dom.VCpu.Content = hypervisor.DefaultMaxCpus
		dom.MaxMem = &maxmem{Unit: "MiB", Slots: "1", Content: hypervisor.DefaultMaxMem}

		cells := make([]cell, 1)
		cells[0].Id = "0"
		cells[0].Cpus = fmt.Sprintf("0-%d", hypervisor.DefaultMaxCpus-1)
		cells[0].Memory = strconv.Itoa(ctx.Boot.Memory * 1024) // older libvirt always considers unit='KiB'
		cells[0].Unit = "KiB"

		dom.CPU.Numa = &numa{Cell: cells}
	}

	cmd, err := exec.LookPath("qemu-system-x86_64")
	if err != nil {
		return "", fmt.Errorf("cannot find qemu-system-x86_64 binary")
	}
	dom.Devices.Emulator = cmd

	dom.OnPowerOff = "destroy"
	dom.OnReboot = "destroy"
	dom.OnCrash = "destroy"

	pcicontroller := controller{
		Type:  "pci",
		Index: "0",
		Model: "pci-root",
	}
	dom.Devices.Controllers = append(dom.Devices.Controllers, pcicontroller)

	serialcontroller := controller{
		Type:  "virtio-serial",
		Index: "0",
		Address: &address{
			Type:     "pci",
			Domain:   "0x0000",
			Bus:      "0x00",
			Slot:     "0x02",
			Function: "0x00",
		},
	}
	dom.Devices.Controllers = append(dom.Devices.Controllers, serialcontroller)

	scsicontroller := controller{
		Type:  "scsi",
		Index: "0",
		Model: "virtio-scsi",
		Address: &address{
			Type:     "pci",
			Domain:   "0x0000",
			Bus:      "0x00",
			Slot:     "0x03",
			Function: "0x00",
		},
	}
	dom.Devices.Controllers = append(dom.Devices.Controllers, scsicontroller)

	usbcontroller := controller{
		Type:  "usb",
		Model: "none",
	}
	dom.Devices.Controllers = append(dom.Devices.Controllers, usbcontroller)

	sharedfs := filesystem{
		Type:       "mount",
		Accessmode: "squash",
		Driver: fsdriver{
			Type: "path",
		},
		Source: fspath{
			Dir: ctx.ShareDir,
		},
		Target: fspath{
			Dir: hypervisor.ShareDirTag,
		},
		Address: &address{
			Type:     "pci",
			Domain:   "0x0000",
			Bus:      "0x00",
			Slot:     "0x04",
			Function: "0x00",
		},
	}
	dom.Devices.Filesystems = append(dom.Devices.Filesystems, sharedfs)

	hyperchannel := channel{
		Type: "unix",
		Source: channsrc{
			Mode: "bind",
			Path: ctx.HyperSockName,
		},
		Target: channtgt{
			Type: "virtio",
			Name: "sh.hyper.channel.0",
		},
	}
	dom.Devices.Channels = append(dom.Devices.Channels, hyperchannel)

	ttychannel := channel{
		Type: "unix",
		Source: channsrc{
			Mode: "bind",
			Path: ctx.TtySockName,
		},
		Target: channtgt{
			Type: "virtio",
			Name: "sh.hyper.channel.1",
		},
	}
	dom.Devices.Channels = append(dom.Devices.Channels, ttychannel)

	dom.Devices.Console = console{
		Type: "unix",
		Source: channsrc{
			Mode: "bind",
			Path: ctx.ConsoleSockName,
		},
		Target: constgt{
			Type: "serial",
			Port: "0",
		},
	}

	dom.Devices.Memballoon = memballoon{
		Model: "virtio",
		Address: &address{
			Type:     "pci",
			Domain:   "0x0000",
			Bus:      "0x00",
			Slot:     "0x05",
			Function: "0x00",
		},
	}

	if boot.Bios != "" && boot.Cbfs != "" {
		dom.OS.Loader = &osloader{
			ReadOnly: "yes",
			Type:     "pflash",
			Content:  boot.Bios,
		}
		dom.OS.Nvram = boot.Cbfs
	} else {
		dom.OS.Kernel = boot.Kernel
		dom.OS.Initrd = boot.Initrd
		dom.OS.Cmdline = "console=ttyS0 panic=1 no_timer_check"
	}

	data, err := xml.Marshal(dom)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (lc *LibvirtContext) Launch(ctx *hypervisor.VmContext) {
	domainXml, err := lc.domainXml(ctx)
	if err != nil {
		glog.Error("Fail to get domain xml configuration:", err)
		ctx.Hub <- &hypervisor.VmStartFailEvent{Message: err.Error()}
		return
	}
	glog.V(3).Infof("domainXML: %v", domainXml)

	domain, err := lc.driver.conn.DomainCreateXML(domainXml, libvirtgo.VIR_DOMAIN_NONE)
	if err != nil {
		glog.Error("Fail to launch domain ", err)
		ctx.Hub <- &hypervisor.VmStartFailEvent{Message: err.Error()}
		return
	}
	lc.domain = &domain
	name, err := lc.domain.GetName()
	if err != nil {
		glog.Error("Fail to get domain name ", err)
		ctx.Hub <- &hypervisor.VmStartFailEvent{Message: err.Error()}
		return
	}
	err = lc.domain.SetMemoryStatsPeriod(1, 0)
	if err != nil {
		glog.Errorf("SetMemoryStatsPeriod failed for domain %v", name)
	}
	lc.driver.domains[name] = ctx
}

func (lc *LibvirtContext) Associate(ctx *hypervisor.VmContext) {
	name, err := lc.domain.GetName()
	if err != nil {
		glog.Error("Fail to get domain name ", err)
		return
	}
	lc.driver.domains[name] = ctx
}

func (lc *LibvirtContext) Dump() (map[string]interface{}, error) {
	if lc.domain == nil {
		return nil, fmt.Errorf("Dom is invalid")
	}

	name, err := lc.domain.GetName()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"hypervisor": "libvirt",
		"name":       name,
	}, nil
}

func (lc *LibvirtContext) Shutdown(ctx *hypervisor.VmContext) {
	go func() {
		if lc.domain == nil {
			ctx.Hub <- &hypervisor.VmExit{}
			return
		}
		name, err := lc.domain.GetName()
		if err != nil {
			return
		}
		lc.domain.DestroyFlags(libvirtgo.VIR_DOMAIN_DESTROY_DEFAULT)
		delete(lc.driver.domains, name)
		ctx.Hub <- &hypervisor.VmExit{}
	}()
}

func (lc *LibvirtContext) Kill(ctx *hypervisor.VmContext) {
	go func() {
		if lc.domain == nil {
			ctx.Hub <- &hypervisor.VmKilledEvent{Success: true}
			return
		}
		name, err := lc.domain.GetName()
		if err != nil {
			return
		}
		lc.domain.DestroyFlags(libvirtgo.VIR_DOMAIN_DESTROY_DEFAULT)
		delete(lc.driver.domains, name)
		ctx.Hub <- &hypervisor.VmKilledEvent{Success: true}
	}()
}

func (lc *LibvirtContext) Close() {
	lc.domain = nil
}

func (lc *LibvirtContext) Pause(ctx *hypervisor.VmContext, cmd *hypervisor.PauseCommand) {
	if lc.domain == nil {
		ctx.Hub <- &hypervisor.PauseResult{Cause: fmt.Sprintf("Cannot find domain"), Reply: cmd}
		return
	}

	var err error

	if cmd.Pause {
		err = lc.domain.Suspend()
	} else {
		err = lc.domain.Resume()
	}

	cause := ""
	if err != nil {
		cause = err.Error()
	}

	ctx.Hub <- &hypervisor.PauseResult{Cause: cause, Reply: cmd}
}

type diskdriver struct {
	Type string `xml:"type,attr"`
	Name string `xml:"name,attr,omitempty"`
}

type monitor struct {
	XMLName xml.Name `xml:"host"`
	Name    string   `xml:"name,attr"`
	Port    string   `xml:"port,attr"`
}

type cephsecret struct {
	Type string `xml:"type,attr"`
	UUID string `xml:"uuid,attr"`
}

type cephauth struct {
	UserName string     `xml:"username,attr"`
	Secret   cephsecret `xml:"secret"`
}

type disksrc struct {
	File     string `xml:"file,attr,omitempty"`
	Protocol string `xml:"protocol,attr,omitempty"`
	Name     string `xml:"name,attr,omitempty"`
	Monitors []monitor
}

type disktgt struct {
	Dev string `xml:"dev,attr,omitempty"`
	Bus string `xml:"bus,attr"`
}

type disk struct {
	XMLName xml.Name    `xml:"disk"`
	Type    string      `xml:"type,attr"`
	Device  string      `xml:"device,attr"`
	Driver  *diskdriver `xml:"driver,omitempty"`
	Source  disksrc     `xml:"source"`
	Target  disktgt     `xml:"target"`
	Address *address    `xml:"address"`
	Auth    *cephauth   `xml:"auth,omitempty"`
}

func diskXml(blockInfo *hypervisor.BlockDescriptor, secretUUID string) (string, error) {
	filename := blockInfo.Filename
	format := blockInfo.Format
	id := blockInfo.ScsiId

	devname := scsiId2Name(id)
	target, unit, err := scsiId2Addr(id)
	if err != nil {
		return "", err
	}

	d := disk{
		Device: "disk",
		Target: disktgt{
			Dev: devname,
			Bus: "scsi",
		},
		Address: &address{
			Type:       "drive",
			Controller: "0",
			Bus:        "0",
			Target:     target,
			Unit:       unit,
		},
	}

	if strings.HasPrefix(filename, "rbd:") {
		if blockInfo.Options == nil {
			return "", fmt.Errorf("Volume options is required for rbd")
		}
		d.Type = "network"
		d.Source = disksrc{
			Protocol: "rbd",
			Name:     strings.TrimPrefix(filename, "rbd:"),
			Monitors: make([]monitor, 0, 1),
		}

		d.Auth = &cephauth{
			UserName: blockInfo.Options["user"],
			Secret: cephsecret{
				Type: "ceph",
				UUID: secretUUID,
			},
		}

		monitors := blockInfo.Options["monitors"]
		for _, m := range strings.Split(monitors, ";") {
			hostport := strings.Split(m, ":")
			d.Source.Monitors = append(d.Source.Monitors, monitor{
				Name: hostport[0],
				Port: hostport[1],
			})
		}

	} else {
		d.Type = "file"
		d.Driver = &diskdriver{Type: format}
		d.Source = disksrc{
			File: filename,
		}
	}

	data, err := xml.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (lc *LibvirtContext) diskSecretUUID(blockInfo *hypervisor.BlockDescriptor) (res string, err error) {
	var sec libvirtgo.VirSecret

	filename := blockInfo.Filename
	if strings.HasPrefix(filename, "rbd:") {
		if blockInfo.Options == nil {
			return "", fmt.Errorf("Volume options is required for rbd")
		}

		username := blockInfo.Options["user"]
		sec, err = lc.driver.conn.LookupSecretByUsage(libvirtgo.VIR_SECRET_USAGE_TYPE_CEPH, "client."+username)
		if err != nil {
			secretXML := fmt.Sprintf("<secret ephemeral='no' private='no'><usage type='ceph'><name>client.%v</name></usage></secret>", username)
			sec, err = lc.driver.conn.SecretDefineXML(secretXML, 0)
			if err != nil {
				return
			}

			res, err = sec.GetUUIDString()
			if err != nil {
				return
			}

			err = lc.driver.conn.SecretSetValue(res, blockInfo.Options["keyring"])
			return
		}

		res, err = sec.GetUUIDString()
	}

	return
}

func scsiId2Name(id int) string {
	return "sd" + utils.DiskId2Name(id)
}

func scsiId2Addr(id int) (int, int, error) {
	if id > 65535 {
		return -1, -1, fmt.Errorf("id %d too long, exceed 256*256", id)
	}

	return id / 256, id % 256, nil
}

func (lc *LibvirtContext) AddDisk(ctx *hypervisor.VmContext, sourceType string, blockInfo *hypervisor.BlockDescriptor) {
	name := blockInfo.Name
	id := blockInfo.ScsiId

	if lc.domain == nil {
		glog.Error("Cannot find domain")
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	secretUUID, err := lc.diskSecretUUID(blockInfo)
	if err != nil {
		glog.Error("generate disk-get-secret failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	diskXml, err := diskXml(blockInfo, secretUUID)
	if err != nil {
		glog.Error("generate attach-disk-xml failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}
	glog.V(3).Infof("diskxml: %s", diskXml)

	err = lc.domain.AttachDeviceFlags(diskXml, libvirtgo.VIR_DOMAIN_DEVICE_MODIFY_LIVE)
	if err != nil {
		glog.Error("attach disk device failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}
	target, unit, err := scsiId2Addr(id)
	ctx.Hub <- &hypervisor.BlockdevInsertedEvent{
		Name:       name,
		SourceType: sourceType,
		DeviceName: scsiId2Name(id),
		ScsiId:     id,
		ScsiAddr:   fmt.Sprintf("%d:%d", target, unit),
	}
}

func (lc *LibvirtContext) RemoveDisk(ctx *hypervisor.VmContext, blockInfo *hypervisor.BlockDescriptor, callback hypervisor.VmEvent) {
	if lc.domain == nil {
		glog.Error("Cannot find domain")
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	secretUUID, err := lc.diskSecretUUID(blockInfo)
	if err != nil {
		glog.Error("generate disk-get-secret failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	diskXml, err := diskXml(blockInfo, secretUUID)
	if err != nil {
		glog.Error("generate detach-disk-xml failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: callback,
		}
		return
	}
	err = lc.domain.DetachDeviceFlags(diskXml, libvirtgo.VIR_DOMAIN_DEVICE_MODIFY_LIVE)
	if err != nil {
		glog.Error("detach disk device failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: callback,
		}
		return
	}
	ctx.Hub <- callback
}

type nicmac struct {
	Address string `xml:"address,attr"`
}

type nicsrc struct {
	Bridge string `xml:"bridge,attr"`
}

type nictgt struct {
	Device string `xml:"dev,attr,omitempty"`
}

type nicmodel fsdriver

type nic struct {
	XMLName xml.Name `xml:"interface"`
	Type    string   `xml:"type,attr"`
	Mac     nicmac   `xml:"mac"`
	Source  nicsrc   `xml:"source"`
	Target  *nictgt  `xml:"target,omitempty"`
	Model   nicmodel `xml:"model"`
	Address *address `xml:"address"`
}

func nicXml(bridge, device, mac string, addr int) (string, error) {
	slot := fmt.Sprintf("0x%x", addr)
	n := nic{
		Type: "bridge",
		Mac: nicmac{
			Address: mac,
		},
		Source: nicsrc{
			Bridge: bridge,
		},
		Target: &nictgt{
			Device: device,
		},
		Model: nicmodel{
			Type: "virtio",
		},
		Address: &address{
			Type:     "pci",
			Domain:   "0x0000",
			Bus:      "0x00",
			Slot:     slot,
			Function: "0x0",
		},
	}

	data, err := xml.Marshal(n)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (lc *LibvirtContext) AddNic(ctx *hypervisor.VmContext, host *hypervisor.HostNicInfo, guest *hypervisor.GuestNicInfo) {
	if lc.domain == nil {
		glog.Error("Cannot find domain")
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	nicXml, err := nicXml(host.Bridge, host.Device, host.Mac, guest.Busaddr)
	if err != nil {
		glog.Error("generate attach-nic-xml failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}
	glog.V(3).Infof("nicxml: %s", nicXml)

	err = lc.domain.AttachDeviceFlags(nicXml, libvirtgo.VIR_DOMAIN_DEVICE_MODIFY_LIVE)
	if err != nil {
		glog.Error("attach nic failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	ctx.Hub <- &hypervisor.NetDevInsertedEvent{
		Index:      guest.Index,
		DeviceName: guest.Device,
		Address:    guest.Busaddr,
	}
}

func (lc *LibvirtContext) RemoveNic(ctx *hypervisor.VmContext, n *hypervisor.InterfaceCreated, callback hypervisor.VmEvent) {
	if lc.domain == nil {
		glog.Error("Cannot find domain")
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: nil,
		}
		return
	}

	nicXml, err := nicXml(n.Bridge, n.HostDevice, n.MacAddr, n.PCIAddr)
	if err != nil {
		glog.Error("generate detach-nic-xml failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: callback,
		}
		return
	}

	err = lc.domain.DetachDeviceFlags(nicXml, libvirtgo.VIR_DOMAIN_DEVICE_MODIFY_LIVE)
	if err != nil {
		glog.Error("detach nic failed, ", err.Error())
		ctx.Hub <- &hypervisor.DeviceFailed{
			Session: callback,
		}
		return
	}
	ctx.Hub <- callback
}

func (lc *LibvirtContext) SetCpus(ctx *hypervisor.VmContext, cpus int, result chan<- error) {
	glog.V(3).Infof("setcpus %d", cpus)
	if lc.domain == nil {
		result <- fmt.Errorf("Cannot find domain")
		return
	}

	err := lc.domain.SetVcpusFlags(uint(cpus), libvirtgo.VIR_DOMAIN_VCPU_LIVE)
	result <- err
}

func (lc *LibvirtContext) AddMem(ctx *hypervisor.VmContext, slot, size int, result chan<- error) {
	memdevXml := fmt.Sprintf("<memory model='dimm'><target><size unit='MiB'>%d</size><node>0</node></target></memory>", size)
	glog.V(3).Infof("memdevXml: %s", memdevXml)
	if lc.domain == nil {
		result <- fmt.Errorf("Cannot find domain")
		return
	}

	err := lc.domain.AttachDeviceFlags(memdevXml, libvirtgo.VIR_DOMAIN_DEVICE_MODIFY_LIVE)
	result <- err
}

func (lc *LibvirtContext) Save(ctx *hypervisor.VmContext, path string, result chan<- error) {
	glog.V(3).Infof("save domain to: %s", path)

	// lc.domain.Save(path) will have libvirt header and will destroy the vm
	// TODO: use virsh qemu-monitor-event to query until completed
	err := exec.Command("virsh", "-c", LibvirtdAddress, "qemu-monitor-command", ctx.Id, "--hmp", fmt.Sprintf("migrate exec:cat>%s", path)).Run()
	result <- err
}
