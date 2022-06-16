package clean

import (
	"context"
	"fmt"

	"github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/client"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Deletes a label from all nodes that have it in the cluster
func DeleteLabel(label string) error {
	nodeList, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=", label)})
	if err != nil {
		return fmt.Errorf("failed to retrieve grandmaster node list %v", err)
	}
	for nodeIndex := range nodeList.Items {
		delete(nodeList.Items[nodeIndex].Labels, label)
		_, err = client.Client.CoreV1().Nodes().Update(context.Background(), &nodeList.Items[nodeIndex], metav1.UpdateOptions{})
		if err != nil {
			logrus.Errorf("Error updating node, err=%s", err)
		}
	}
	return nil
}

// All removes any configuration applied by ptp tests.
func All() error {
	Configs()

	err := DeleteLabel(utils.PtpGrandmasterNodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpGrandmasterNodeLabel, err)
	}
	err = DeleteLabel(utils.PtpSlaveNodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpSlaveNodeLabel, err)
	}
	err = DeleteLabel(utils.PtpBCMasterNodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpBCMasterNodeLabel, err)
	}
	err = DeleteLabel(utils.PtpBCSlaveNodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpBCSlaveNodeLabel, err)
	}
	return nil
}

func Configs() {
	ptpconfigList, err := client.Client.PtpConfigs(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logrus.Errorf("clean.All: Failed to retrieve ptp config list %v", err)
	}

	for _, ptpConfig := range ptpconfigList.Items {
		if ptpConfig.Name == utils.PtpGrandMasterPolicyName ||
			ptpConfig.Name == utils.PtpSlavePolicyName ||
			ptpConfig.Name == utils.PtpBcMasterPolicyName ||
			ptpConfig.Name == utils.PtpBcSlavePolicyName {
			err = client.Client.PtpConfigs(utils.PtpLinuxDaemonNamespace).Delete(context.Background(), ptpConfig.Name, metav1.DeleteOptions{})
			if err != nil {
				logrus.Errorf("clean.All: Failed to delete ptp config %s %v", ptpConfig.Name, err)
			}
		}
	}
}
