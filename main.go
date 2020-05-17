package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path"
	"strconv"
	"time"

	docker "github.com/fsouza/go-dockerclient"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/cobra"
	"github.com/teris-io/shortid"
)

func archiveGitRepo(repo *git.Repository, hash plumbing.Hash, tarWriter *io.PipeWriter) {
	tr := tar.NewWriter(tarWriter)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		log.Fatal(err)
	}
	files, err := commit.Files()
	if err != nil {
		log.Fatal(err)
	}
	t := time.Now()
	for {
		file, err := files.Next()
		if err != nil {
			if err != io.EOF {
				log.Fatal(err)
			}
			break
		}
		tr.WriteHeader(&tar.Header{Name: file.Name, Size: file.Size, ModTime: t, AccessTime: t, ChangeTime: t})
		reader, err := file.Blob.Reader()
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(tr, reader)
	}
	tr.Close()
	tarWriter.Close()
}

func deployRepo(appName string, repoDir string) {
	dockerClient, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		fmt.Println("Could not connect to docker at /var/run/docker.sock. Is docker running?")
		os.Exit(1)
	}

	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		log.Fatal(err)
	}
	hash, err := repo.ResolveRevision("master")
	if err != nil {
		log.Fatal(err)
	}
	gitRef := hash.String()

	tarReader, tarWriter := io.Pipe()
	go archiveGitRepo(repo, *hash, tarWriter)

	imageName := "dokun/" + appName

	labels := map[string]string{
		"io.dokun.app":    appName,
		"io.dokun.gitRef": gitRef,
	}

	buildOutput := bytes.NewBuffer(nil)
	opts := docker.BuildImageOptions{
		Name:         imageName,
		InputStream:  tarReader,
		OutputStream: buildOutput,
		Labels:       labels,
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
			"label": []string{"io.dokun.app=" + appName},
		},
	}

	previousContainers, err := dockerClient.ListContainers(listOpts)
	if err != nil {
		panic(err)
	}

	fmt.Println("Starting new containers...")

	containerName := appName + "-" + uuid

	createOpts := docker.CreateContainerOptions{
		Name: containerName,
		Config: &docker.Config{
			Image:  imageName + ":latest",
			Labels: labels,
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
			"label":  []string{"io.dokun.app=" + appName},
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

func createApp(app string, noUserWarn bool) {
	uid := os.Geteuid()
	usr, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		panic(err)
	}

	if usr.Username != "dokun" && !noUserWarn {
		fmt.Println("Running as non-dokun user. Have you enabled setuid on dokun executable and set the owner of the executable as dokun?")
		os.Exit(1)
	}

	repoPath := path.Join(usr.HomeDir, app+".git")

	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	_, err = git.PlainInit(repoPath, true)
	if err != nil {
		panic(err)
	}

	postReceiveScript := []byte("#!/bin/sh\n\ndokun deploy-repo " + app + " \"$(pwd)\"\n")

	hooksDir := path.Join(repoPath, "hooks")
	err = os.Mkdir(hooksDir, 0755)
	if err != nil {
		panic(err)
	}

	hookPath := path.Join(hooksDir, "post-receive")
	err = ioutil.WriteFile(hookPath, postReceiveScript, 0755)
	if err != nil {
		panic(err)
	}

	fmt.Println("Ready. Add the remote to your project:")
	fmt.Println("\t$ git remote add " + usr.Username + "@" + hostname + ":/" + app + ".git")
}

func destroyApp(app string, noUserWarn bool) {
	uid := os.Geteuid()
	usr, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		log.Fatal(err)
	}

	if usr.Username != "dokun" && !noUserWarn {
		fmt.Println("Running as non-dokun user. Have you enabled setuid on dokun executable and set the owner of the executable as doku?")
		os.Exit(1)
	}

	repoPath := path.Join(usr.HomeDir, app+".git")

	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		log.Fatal("No such application: " + app)
	}

	fmt.Println("This operation will destroy the git repository at " + repoPath + " and all of the associated docker containers and images.")
	fmt.Println("For confirmation, please type the name of the application (" + app + "):")

	var confirmationResponse string
	_, err = fmt.Scanln(&confirmationResponse)
	if confirmationResponse != app {
		fmt.Println(confirmationResponse + "!=" + app + ". Exiting without destroying application.")
		os.Exit(0)
	}

	dockerClient, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		log.Fatal(err)
	}

	listOpts := docker.ListContainersOptions{
		All: true,
		Filters: map[string][]string{
			"label": []string{"io.dokun.app=" + app},
		},
	}

	ctx := context.TODO()

	fmt.Println("Removing containers...")

	containers, err := dockerClient.ListContainers(listOpts)
	if err != nil {
		log.Fatal(err)
	}

	for _, container := range containers {
		removeOpts := docker.RemoveContainerOptions{
			ID:            container.ID,
			RemoveVolumes: false,
			Force:         true,
			Context:       ctx,
		}
		dockerClient.RemoveContainer(removeOpts)
	}

	fmt.Println("Removing images...")

	imageListOpts := docker.ListImagesOptions{
		Filters: map[string][]string{
			"label": []string{"io.dokun.app=" + app},
		},
	}
	images, err := dockerClient.ListImages(imageListOpts)
	if err != nil {
		log.Fatal(err)
	}

	for _, image := range images {
		dockerClient.RemoveImage(image.ID)
	}

	fmt.Println("Destroying " + repoPath + "...")
	err = os.RemoveAll(repoPath)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	var noUserWarn bool

	var cmdDeployRepo = &cobra.Command{
		Use:    "deploy-repo [app] [path]",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			appName := args[0]
			repoDir := args[1]
			fmt.Println("Deploying: " + appName)
			deployRepo(appName, repoDir)
		},
	}

	var cmdCreateApp = &cobra.Command{
		Use:   "create [app]",
		Short: "Initializes git repository for an application.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			createApp(args[0], noUserWarn)
		},
	}

	var cmdDestroyApp = &cobra.Command{
		Use:   "destroy [app]",
		Short: "Removes git repository and cleans docker images and containers.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			destroyApp(args[0], noUserWarn)
		},
	}

	var rootCmd = &cobra.Command{Use: "cmd"}
	rootCmd.PersistentFlags().BoolVarP(&noUserWarn, "no-user-warn", "u", false, "disable warning when setuid not set")
	rootCmd.AddCommand(cmdDeployRepo, cmdCreateApp, cmdDestroyApp)
	rootCmd.Execute()
}
