package main

import (
	"fmt"
	"path"
)

// Masks - маски имён файлов (только basename, путь не учитывается)
var Masks = []string{
	"*values*.yaml", "*values*.yml",
	"Chart.yaml",
	"*helmfile*.yaml", "*helmfile*.yml",
	"*Dockerfile*", "*.dockerfile",
	"*compose*.yml", "*compose*.yaml",
	"*docker-bake*.hcl", "*docker-bake*.json",
	"*stack*.yaml", "*stack*.yml",
	"*deployment*.yaml", "*statefulset*.yaml", "*daemonset*.yaml",
	"*job*.yaml", "*pod*.yaml", "*replicaset*.yaml",
	"kustomization.yaml", "kustomization.yml", "Kustomization",
	"*application*.yaml", "*helmrelease*.yaml",
	"*skaffold*.yaml", "*skaffold*.yml", "Tiltfile",
	"*gitlab-ci*.yml", "*Jenkinsfile*", "*azure-pipelines*.yml",
	"bitbucket-pipelines.yml", "*cloudbuild*.yaml",
	"*buildspec*.yml", "*buildspec*.yaml",
	"*taskdef*.json", "*task-definition*.json", "service.yaml",
	"*pom*.xml", "*docker*.xml", "*assembly*.xml",
}

// MatchAny сверяет ИМЯ файла (без пути) с масками из списка.
func MatchAny(filePath string, masks []string) bool {
	name := path.Base(filePath)
	for _, m := range masks {
		if ok, _ := path.Match(m, name); ok {
			return true
		}
	}
	return false
}

func main() {
	tests := []string{
		"some/path/some-values.yaml",
		"values.yaml",
		"docker/Dockerfile.prod",
		"prod-docker-compose.yml",
		"app-compose.yaml",
		"backend-deployment.yaml",
		"api-cronjob.yaml",
		"frontend-application.yaml",
		"apps/applicationset-root.yaml",
		"Jenkinsfile.staging",
		"cloudbuild-prod.yaml",
		"buildspec-prod.yml",
		"infra/kustomization.yml",
		"infra/Kustomization",
		"backend/pom.xml",
		"pom-prod.xml",
		"module/docker-assembly-prod.xml",
		"ecs/service-taskdef-prod.json",
		"src/main.go",
	}
	for _, t := range tests {
		fmt.Printf("%-40s -> %v\n", t, MatchAny(t, Masks))
	}
}