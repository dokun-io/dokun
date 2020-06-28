# Dokun

Self-hosted git push to docker build workflow.

__This is still under heavy development. Use it at your own risk.__

## Prerequisites

Make sure you have installed the following software on your server:

 * git
 * docker

## Installation

```
$ useradd dokun -d /home/dokun -m
$ usermod -G dokun
$ cp dokun /usr/local/bin
$ sudo chown dokun /usr/local/bin/dokun
$ sudo chmod u+s /usr/local/bin/dokun
```

In order to deploy via ssh, also copy your public ssh key to /home/dokun/.ssh/authorized_keys.

## Features

* Single small binary deployment
* Play well with other applications using docker on the same server.
* No buildpacks, just use docker and dockerfiles.
* Runtime configuration (port, environment, etc) in git repository (TODO).

## Usage

### dokun create *app*

Creates a git repository at /home/dokun/app.git.

After that you can `git push dokun@yourhost:app.git` to start docker build.

Your project should contain a Dockerfile. You can only set environment, ports etc on the Dockerfile, there is no way (yet) to set runtime options (it's next on the list).

### dokun destroy *app*

Destroy git repository at /home/dokun/app.git, and cleans up docker resources.
