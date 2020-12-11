// Copyright 2018-2020 Xilinx Corporation. All Rights Reserved.
// Author: Brian Xu(brianx@xilinx.com)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"io/ioutil"
	pluginapi "k8s.io/kubernetes/pkg/kubelet/apis/deviceplugin/v1beta1"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	SysfsDevices   = "/sys/bus/pci/devices"
	UserPrefix     = "/dev/dri"
	SubdevPrefix   = "/dev/xfpga"
	QDMASTR        = "dma.qdma.u"
	UserPFKeyword  = "drm"
	DRMSTR         = "renderD"
	ROMSTR         = "rom.u"
	DSAverFile     = "VBNV"
	DSAtsFile      = "timestamp"
	XMCSTR         = "xmc.u"
	SerialNumFile  = "serial_num"
	MgmtFile       = "mgmt_pf"
	UserFile       = "user_pf"
	VendorFile     = "vendor"
	DeviceFile     = "device"
	ReadyFile      = "ready"
	FPGAReady      = "0x1"
	XilinxVendorID = "0x10ee"
	ADVANTECH_ID   = "0x13fe"
	AWS_ID         = "0x1d0f"
)

type Node struct {
	User       string
	SubdevPath string
	Qdma       string
	DBDF       string // this is for user pf
	deviceID   string //devid of the user pf
}

type Device struct {
	sn        string
	shellVer  string
	timestamp string
	Healthy   string
	Nodes     []Node
}

func GetInstance(DBDF string) (string, error) {
	strArray := strings.Split(DBDF, ":")
	domain, err := strconv.ParseUint(strArray[0], 16, 16)
	if err != nil {
		return "", fmt.Errorf("strconv failed: %s", strArray[0])
	}
	bus, err := strconv.ParseUint(strArray[1], 16, 8)
	if err != nil {
		return "", fmt.Errorf("strconv failed: %s", strArray[1])
	}
	strArray = strings.Split(strArray[2], ".")
	dev, err := strconv.ParseUint(strArray[0], 16, 8)
	if err != nil {
		return "", fmt.Errorf("strconv failed: %s", strArray[0])
	}
	fc, err := strconv.ParseUint(strArray[1], 16, 8)
	if err != nil {
		return "", fmt.Errorf("strconv failed: %s", strArray[1])
	}
	ret := domain*65536 + bus*256 + dev*8 + fc
	return strconv.FormatUint(ret, 10), nil
}

func GetFileNameFromPrefix(dir string, prefix string) (string, error) {
	userFiles, err := ioutil.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("Can't read folder %s", dir)
	}
	for _, userFile := range userFiles {
		fname := userFile.Name()

		if !strings.HasPrefix(fname, prefix) {
			continue
		}
		return fname, nil
	}
	return "", nil
}

func GetFileContent(file string) (string, error) {
	if buf, err := ioutil.ReadFile(file); err != nil {
		return "", fmt.Errorf("Can't read file %s", file)
	} else {
		ret := strings.Trim(string(buf), "\n")
		return ret, nil
	}
}

//Prior to 2018.3 release, Xilinx FPGA has mgmt PF as func 1 and user PF
//as func 0. The func numbers of the 2 PFs are swapped after 2018.3 release.
//The FPGA device driver in (and after) 2018.3 release creates sysfs file --
//mgmt_pf and user_pf accordingly to reflect what a PF really is.
//
//The plugin will rely on this info to determine whether the a entry is mgmtPF,
//userPF, or none. This also means, it will not support 2018.2 any more.
func FileExist(fname string) bool {
	if _, err := os.Stat(fname); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func IsMgmtPf(pciID string) bool {
	fname := path.Join(SysfsDevices, pciID, MgmtFile)
	return FileExist(fname)
}

func IsUserPf(pciID string) bool {
	fname := path.Join(SysfsDevices, pciID, UserFile)
	return FileExist(fname)
}

func GetDevices() (map[string]Device, error) {
	devices := make(map[string]Device)
	pciFiles, err := ioutil.ReadDir(SysfsDevices)
	if err != nil {
		return nil, fmt.Errorf("Can't read folder %s", SysfsDevices)
	}

	for _, pciFile := range pciFiles {
		pciID := pciFile.Name()

		fname := path.Join(SysfsDevices, pciID, VendorFile)
		vendorID, err := GetFileContent(fname)
		if err != nil {
			continue
		}
		if strings.EqualFold(vendorID, XilinxVendorID) != true &&
			strings.EqualFold(vendorID, AWS_ID) != true &&
			strings.EqualFold(vendorID, ADVANTECH_ID) != true {
			continue
		}

		// For containers deployed either on top of baremetal machines,
		// or deployed on top of VM, there may be only user PF assigned
		// to vm(mgmt PF is not assigned to the VM)
		if IsUserPf(pciID) { //user pf
			userDBDF := pciID
			// skip FPGAs that are not ready
			fname = path.Join(SysfsDevices, pciID, ReadyFile)
			content, err := GetFileContent(fname)
			if err != nil {
				continue
			}
			if strings.Compare(content, FPGAReady) != 0 {
				continue
			}
			// get SN
			xmcFolder, err := GetFileNameFromPrefix(path.Join(SysfsDevices, pciID), XMCSTR)
			if err != nil {
				continue
			}
			fname = path.Join(SysfsDevices, pciID, xmcFolder, SerialNumFile)
			content, err = GetFileContent(fname)
			if err != nil {
				continue
			}
			sn := content
			// get dsa version
			romFolder, err := GetFileNameFromPrefix(path.Join(SysfsDevices, pciID), ROMSTR)
			if err != nil {
				continue
			}
			fname = path.Join(SysfsDevices, pciID, romFolder, DSAverFile)
			content, err = GetFileContent(fname)
			if err != nil {
				continue
			}
			dsaVer := content
			// get dsa timestamp
			fname = path.Join(SysfsDevices, pciID, romFolder, DSAtsFile)
			content, err = GetFileContent(fname)
			if err != nil {
				continue
			}
			dsaTs := content
			// get device id
			fname = path.Join(SysfsDevices, pciID, DeviceFile)
			content, err = GetFileContent(fname)
			if err != nil {
				continue
			}
			devid := content
			// get user PF node
			userpf, err := GetFileNameFromPrefix(path.Join(SysfsDevices, pciID, UserPFKeyword), DRMSTR)
			if err != nil {
				continue
			}
			userNode := path.Join(UserPrefix, userpf)

			node := Node{
				DBDF:       userDBDF,
				deviceID:   devid,
				User:       userNode,
				SubdevPath: SubdevPrefix,
				Qdma:       "",
			}

			//get qdma device node if it exists
			instance, err := GetInstance(userDBDF)
			if err != nil {
				continue
			}

			qdmaFolder, err := GetFileNameFromPrefix(path.Join(SysfsDevices, pciID), QDMASTR)
			if err != nil {
				continue
			}

			if qdmaFolder != "" {
				node.Qdma = path.Join(SubdevPrefix, QDMASTR+instance)
			}

			//TODO: check temp, power, fan speed etc, to give a healthy level
			//so far, return Healthy
			healthy := pluginapi.Healthy

			if _, ok := devices[sn]; ok {
				device := devices[sn]
				nodes := device.Nodes
				nodes = append(nodes, node)
				device.Nodes = nodes
				devices[sn] = device
			} else {
				devices[sn] = Device{
					sn:        sn,
					shellVer:  dsaVer,
					timestamp: dsaTs,
					Healthy:   healthy,
					Nodes:     []Node{node},
				}
			}
		}
	}
	return devices, nil
}

/*
func main() {
	devices, err := GetDevices()
	if err != nil {
		fmt.Printf("%s !!!\n", err)
		return
	}
	for sn, device := range devices {
		fmt.Printf("S/N: %s\n", sn)
		fmt.Printf("%v\n", device)
	}
}
*/
