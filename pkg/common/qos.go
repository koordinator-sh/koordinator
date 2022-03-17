package common

import corev1 "k8s.io/api/core/v1"

type QoSClass string

const (
	QoSLSR    QoSClass = "LSR"
	QoSLS     QoSClass = "LS"
	QoSBE     QoSClass = "BE"
	QoSSystem QoSClass = "SYSTEM"
	QoSNone   QoSClass = ""
)

func GetPodQoSClass(pod *corev1.Pod) QoSClass {
	if pod == nil || pod.Labels == nil {
		return QoSNone
	}

	if q, ok := pod.Labels[LabelPodQoS]; ok {
		return getPodQoSClassByName(q)
	}

	return QoSNone
}

func getPodQoSClassByName(qos string) QoSClass {
	q := QoSClass(qos)

	switch q {
	case QoSLSR, QoSLS, QoSBE, QoSSystem:
		return q
	}

	return QoSNone
}
