package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
)

type activeScope struct {
	region    string
	namespace string
}

func currentActiveScope() (*activeScope, error) {
	root, err := auth.CurrentSealtunDir()
	if err != nil {
		return nil, err
	}
	authData, err := auth.LoadAuthDataFromDir(root)
	if err != nil {
		return nil, fmt.Errorf("not logged in. Please run 'sealtun login' first: %w", err)
	}
	kubeconfigPath := filepath.Join(root, "kubeconfig")
	if _, err := readActiveRegularFile(kubeconfigPath, "active kubeconfig"); err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig: %w", err)
	}
	client, err := k8s.NewClient(kubeconfigPath, authData)
	if err != nil {
		return nil, fmt.Errorf("failed to init k8s client: %w", err)
	}
	return &activeScope{region: authData.Region, namespace: client.Namespace()}, nil
}

func readActiveRegularFile(path, label string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %s is not a regular file", label, path)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the active Sealtun kubeconfig under the configured home.
	if err != nil {
		return "", err
	}
	return string(data), nil
}
