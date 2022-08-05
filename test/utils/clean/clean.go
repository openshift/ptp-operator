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
			return fmt.Errorf("error updating node, err=%s", err)
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
	err = DeleteLabel(utils.PtpClockUnderTestNodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpClockUnderTestNodeLabel, err)
	}
	err = DeleteLabel(utils.PtpSlave1NodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpSlave1NodeLabel, err)
	}
	err = DeleteLabel(utils.PtpSlave2NodeLabel)
	if err != nil {
		return fmt.Errorf("clean.All: fail to delete label: %s, err: %s", utils.PtpSlave2NodeLabel, err)
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
			ptpConfig.Name == utils.PtpBcMaster1PolicyName ||
			ptpConfig.Name == utils.PtpSlave1PolicyName ||
			ptpConfig.Name == utils.PtpBcMaster2PolicyName ||
			ptpConfig.Name == utils.PtpSlave2PolicyName ||
			ptpConfig.Name == utils.PtpTempPolicyName {
			err = client.Client.PtpConfigs(utils.PtpLinuxDaemonNamespace).Delete(context.Background(), ptpConfig.Name, metav1.DeleteOptions{})
			if err != nil {
				logrus.Errorf("clean.All: Failed to delete ptp config %s %v", ptpConfig.Name, err)
			}
		}
	}
}
