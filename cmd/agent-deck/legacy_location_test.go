package main

import "github.com/RishabhKodes/agent-deck/internal/session"

const controllerCWD = "/home/dev/controller-cwd"

func sshInstance(id, title, host, remotePath string) *session.Instance {
	return &session.Instance{
		ID:            id,
		Title:         title,
		ProjectPath:   controllerCWD,
		SSHHost:       host,
		SSHRemotePath: remotePath,
	}
}
