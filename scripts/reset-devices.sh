#!/bin/bash
set -x
set -euo pipefail

modprobe -r netdevsim 
modprobe -r openvswitch 
modprobe netdevsim 
modprobe openvswitch