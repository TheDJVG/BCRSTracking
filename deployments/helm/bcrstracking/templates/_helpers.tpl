{{/*
Expand the name of the chart.
*/}}
{{- define "bcrstracking.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "bcrstracking.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "bcrstracking.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "bcrstracking.labels" -}}
helm.sh/chart: {{ include "bcrstracking.chart" . }}
{{ include "bcrstracking.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "bcrstracking.selectorLabels" -}}
app.kubernetes.io/name: {{ include "bcrstracking.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "bcrstracking.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "bcrstracking.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
ClickHouse cluster name
*/}}
{{- define "bcrstracking.clickhouseClusterName" -}}
{{- default (printf "%s-clickhouse" (include "bcrstracking.fullname" .)) .Values.clickhouseCluster.cluster.name }}
{{- end }}

{{/*
ClickHouse keeper name
*/}}
{{- define "bcrstracking.clickhouseKeeperName" -}}
{{- default (printf "%s-keeper" (include "bcrstracking.fullname" .)) .Values.clickhouseCluster.keeper.name }}
{{- end }}

{{/*
ClickHouse connection address
*/}}
{{- define "bcrstracking.clickhouseAddr" -}}
{{- if .Values.config.clickhouseAddr }}
{{- .Values.config.clickhouseAddr }}
{{- else if .Values.clickhouseCluster.enabled }}
{{- printf "%s-clickhouse-headless:9000" (include "bcrstracking.clickhouseClusterName" .) }}
{{- else }}
{{- "clickhouse.default.svc.cluster.local:9000" }}
{{- end }}
{{- end }}
