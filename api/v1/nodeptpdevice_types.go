/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// NodePtpDevice is the Schema for the nodeptpdevices API
type NodePtpDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodePtpDeviceSpec   `json:"spec,omitempty"`
	Status NodePtpDeviceStatus `json:"status,omitempty"`
}

// NodePtpDeviceSpec defines the desired state of NodePtpDevice
type NodePtpDeviceSpec struct {
}

type PtpDevice struct {
	// Name is the name of the PTP device.
	// It is a unique identifier for the device.
	// +optional
	Name string `json:"name,omitempty"`

	// Profile is the PTP profile associated with the device.
	// This profile defines the PTP configuration settings for the device.
	// +optional
	Profile string `json:"profile,omitempty"`

	// HardwareInfo contains detailed hardware identification information for the device.
	// +optional
	HardwareInfo *HardwareInfo `json:"hardwareInfo,omitempty"`
}

// HardwareInfo contains detailed hardware identification and characteristics of a PTP device
type HardwareInfo struct {
	// PCI Information - Device location and identification on the PCI bus

	// PCIAddress is the PCI bus address
	// +optional
	PCIAddress string `json:"pciAddress,omitempty"`

	// VendorID is the PCI vendor identifier
	// +optional
	VendorID string `json:"vendorID,omitempty"`

	// DeviceID is the PCI device identifier
	// +optional
	DeviceID string `json:"deviceID,omitempty"`

	// SubsystemVendorID is the PCI subsystem vendor identifier
	// +optional
	SubsystemVendorID string `json:"subsystemVendorID,omitempty"`

	// SubsystemDeviceID is the PCI subsystem device identifier
	// +optional
	SubsystemDeviceID string `json:"subsystemDeviceID,omitempty"`

	// Firmware and Driver Information

	// FirmwareVersion is the version of the device firmware
	// +optional
	FirmwareVersion string `json:"firmwareVersion,omitempty"`

	// DriverVersion is the version of the kernel driver in use
	// +optional
	DriverVersion string `json:"driverVersion,omitempty"`

	// Link Status Information

	// LinkStatus indicates whether the link is up ("up") or down ("down")
	// +optional
	LinkStatus string `json:"linkStatus,omitempty"`

	// LinkSpeed is the negotiated link speed
	// +optional
	LinkSpeed string `json:"linkSpeed,omitempty"`

	// FEC is the Forward Error Correction mode in use
	// +optional
	FEC string `json:"fec,omitempty"`

	// VPD (Vital Product Data) - Manufacturing and product information

	// VPDIdentifierString is the device identifier string from VPD
	// +optional
	VPDIdentifierString string `json:"vpdIdentifierString,omitempty"`

	// VPDPartNumber is the manufacturer's part number from VPD
	// +optional
	VPDPartNumber string `json:"vpdPartNumber,omitempty"`

	// VPDSerialNumber is the unique serial number from VPD
	// +optional
	VPDSerialNumber string `json:"vpdSerialNumber,omitempty"`

	// VPDManufacturerID is the manufacturer identifier from VPD
	// +optional
	VPDManufacturerID string `json:"vpdManufacturerID,omitempty"`

	// VPDProductName is the product name from VPD (V0 field)
	// +optional
	VPDProductName string `json:"vpdProductName,omitempty"`

	// VPDVendorSpecific1 is vendor-specific data from VPD (V1 field).
	// Often contains detailed product identification useful for hardware fingerprinting.
	// +optional
	VPDVendorSpecific1 string `json:"vpdVendorSpecific1,omitempty"`

	// VPDVendorSpecific2 is vendor-specific data from VPD (V2 field)
	// +optional
	VPDVendorSpecific2 string `json:"vpdVendorSpecific2,omitempty"`
}

type HwConfig struct {
	// DeviceID is the unique identifier for the hardware device.
	// +optional
	DeviceID string `json:"deviceID,omitempty"`

	// VendorID is the identifier for the vendor of the hardware device.
	// +optional
	VendorID string `json:"vendorID,omitempty"`

	// Failed indicates whether the hardware configuration has failed.
	// A value of true means the configuration has failed.
	// +optional
	Failed bool `json:"failed,omitempty"`

	// Status provides a descriptive status of the hardware device's configuration.
	// +optional
	Status string `json:"status,omitempty"`

	// Config contains the configuration settings for the hardware device.
	// This is a JSON object that holds the device-specific configuration.
	// +optional
	Config *apiextensions.JSON `json:"config,omitempty"`
}

// NodePtpDeviceStatus defines the observed state of NodePtpDevice
type NodePtpDeviceStatus struct {

	// PtpDevice represents a PTP device available in the cluster node.
	// This struct contains information about the device, including its name and profile.
	// +optional
	Devices []PtpDevice `json:"devices,omitempty"`

	// HwConfig represents the hardware configuration for a device in the cluster.
	// This struct contains information about the device's identification and status,
	// as well as its specific configuration settings.
	// +optional
	Hwconfig []HwConfig `json:"hwconfig,omitempty"`
}

//+kubebuilder:object:root=true

// NodePtpDeviceList contains a list of NodePtpDevice
type NodePtpDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodePtpDevice `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodePtpDevice{}, &NodePtpDeviceList{})
}
