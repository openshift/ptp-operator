package pods

import (
	"bytes"
	"context"
	"io"

	"github.com/redhat-cne/l2discovery-lib/pkg/l2client"
	corev1 "k8s.io/api/core/v1"
)

// GetLog connects to a pod and fetches log
func GetLog(p *corev1.Pod, containerName string) (string, error) {
	req := l2client.Client.K8sClient.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{Container: containerName})
	log, err := req.Stream(context.Background())
	if err != nil {
		return "", err
	}
	defer log.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, log)

	if err != nil {
		return "", err
	}

	return buf.String(), nil
}
