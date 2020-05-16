package main

import (
	//"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	// "time"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/spf13/cobra"
	"github.com/teris-io/shortid"
)

func deploy(projectName string, repoDir string) {
	dockerClient, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		log.Fatal(err)
	}

	revParse := exec.Command("git", "rev-parse", "--short", "master")
	revParse.Dir = repoDir
	revParseOutput, err := revParse.Output()
	if err != nil {
		panic(err)
	}

	gitRef := strings.TrimSpace(string(revParseOutput))

	archive := exec.Command("git", "archive", "master")
	archive.Dir = repoDir
	archiveOutput, err := archive.Output()
	if err != nil {
		panic(err)
	}

	imageName := projectName

	buildInput := bytes.NewReader(archiveOutput)
	buildOutput := bytes.NewBuffer(nil)
	opts := docker.BuildImageOptions{
		Name:         imageName,
		InputStream:  buildInput,
		OutputStream: buildOutput,
	}
	if err := dockerClient.BuildImage(opts); err != nil {
		log.Fatal(err)
	}
	scanner := bufio.NewScanner(buildOutput)
	for scanner.Scan() {
		m := scanner.Text()
		fmt.Println(m)
	}

	ctx := context.TODO()

	uuid, err := shortid.Generate()
	if err != nil {
		panic(err)
	}

	listOpts := docker.ListContainersOptions{
		Filters: map[string][]string{
			"label": []string{"io.dokun.project=" + projectName},
		},
	}

	previousContainers, err := dockerClient.ListContainers(listOpts)
	if err != nil {
		panic(err)
	}

	fmt.Println("Starting new containers...")

	containerName := projectName + "-" + uuid

	createOpts := docker.CreateContainerOptions{
		Name: containerName,
		Config: &docker.Config{
			Image: imageName + ":latest",
			Labels: map[string]string{
				"io.dokun.project": projectName,
				"io.dokun.gitRef":  gitRef,
			},
		},
		HostConfig:       &docker.HostConfig{},
		NetworkingConfig: &docker.NetworkingConfig{},
		Context:          ctx,
	}
	container, err := dockerClient.CreateContainer(createOpts)
	if err != nil {
		panic(err)
	}

	err = dockerClient.StartContainer(container.ID, &docker.HostConfig{})
	if err != nil {
		panic(err)
	}

	fmt.Println("Stopping previous containers...")

	for _, prevContainer := range previousContainers {
		// TODO: concurrently
		fmt.Println("Stopping container " + prevContainer.ID + "...")
		dockerClient.StopContainerWithContext(prevContainer.ID, 10, ctx)
	}

	fmt.Println("Removing exited containers...")

	listOpts = docker.ListContainersOptions{
		All: true,
		Filters: map[string][]string{
			"status": []string{"exited"},
			"label":  []string{"io.dokun.project=" + projectName},
		},
	}
	exitedContainers, err := dockerClient.ListContainers(listOpts)
	if err != nil {
		panic(err)
	}

	for _, exitedContainer := range exitedContainers {
		removeOpts := docker.RemoveContainerOptions{
			ID:            exitedContainer.ID,
			RemoveVolumes: false,
			Force:         false,
			Context:       ctx,
		}
		dockerClient.RemoveContainer(removeOpts)
	}

	fmt.Println("Ready")
}

func main() {
	var cmdDeployRepo = &cobra.Command{
		Use:    "deploy-repo [project] [path]",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			projectName := args[0]
			repoDir := args[1]
			fmt.Println("Deploying: " + projectName)
			deploy(projectName, repoDir)
		},
	}

	var rootCmd = &cobra.Command{Use: "cmd"}
	rootCmd.AddCommand(cmdDeployRepo)
	rootCmd.Execute()
}
