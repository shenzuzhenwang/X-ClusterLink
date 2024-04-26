/*
Copyright 2024.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GatewayExIpSpec defines the desired state of GatewayExIp
type GatewayExIpSpec struct {
	ExternalIP    string `json:"externalIp,omitempty"`
	GlobalNetCIDR string `json:"globalNetCIDR,omitempty"`
}

// GatewayExIpStatus defines the observed state of GatewayExIp
type GatewayExIpStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// GatewayExIp is the Schema for the gatewayexips API
type GatewayExIp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GatewayExIpSpec   `json:"spec,omitempty"`
	Status GatewayExIpStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GatewayExIpList contains a list of GatewayExIp
type GatewayExIpList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GatewayExIp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GatewayExIp{}, &GatewayExIpList{})
}
