{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "koordinator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "koordinator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "koordinator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "koordinator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "koordinator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Lookup existing immutatble resources
*/}}
{{- define "webhookServiceSpec" -}}
{{- $service := lookup "v1" "Service" .Values.installation.namespace "koordinator-webhook-service" -}}
{{- if $service -}}
{{ if $service.spec.clusterIP -}}
clusterIP: {{ $service.spec.clusterIP }}
{{- end }}
{{ if $service.spec.clusterIPs -}}
clusterIPs:
  {{ $service.spec.clusterIPs }}
{{- end }}
{{ if $service.spec.ipFamilyPolicy -}}
ipFamilyPolicy: {{ $service.spec.ipFamilyPolicy }}
{{- end }}
{{ if $service.spec.ipFamilies -}}
ipFamilies:
  {{ $service.spec.ipFamilies }}
{{- end }}
{{ if $service.spec.type -}}
type: {{ $service.spec.type }}
{{- end }}
{{ if $service.spec.ipFamily -}}
ipFamily: {{ $service.spec.ipFamily }}
{{- end }}
{{- end -}}
ports:
- port: 443
  targetPort: {{ .Values.manager.webhook.port }}
selector:
  koord-app: koord-manager
{{- end -}}

{{- define "webhookSecretData" -}}
{{- $secret := lookup "v1" "Secret" .Values.installation.namespace "koordinator-webhook-certs" -}}
{{- if $secret -}}
data:
{{- range $k, $v := $secret.data }}
  {{ $k }}: {{ $v }}
{{- end }}
{{- end }}
{{- end -}}

{{- define "serviceAccountManager" -}}
{{- $sa := lookup "v1" "ServiceAccount" .Values.installation.namespace "koord-manager" -}}
{{- if $sa -}}
secrets:
{{- range $v := $sa.secrets }}
- name: {{ $v.name }}
{{- end }}
{{- end }}
{{- end -}}
