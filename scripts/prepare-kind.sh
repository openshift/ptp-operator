kubectl label node kind-netdevsim-control-plane node-role.kubernetes.io/control-plane=true
kubectl label node kind-netdevsim-worker  node-role.kubernetes.io/master= --overwrite
kubectl label node kind-netdevsim-worker2 node-role.kubernetes.io/master= --overwrite
kubectl label node kind-netdevsim-worker3 node-role.kubernetes.io/master= --overwrite

kubectl label node kind-netdevsim-worker node-role.kubernetes.io/worker= --overwrite
kubectl label node kind-netdevsim-worker2 node-role.kubernetes.io/worker= --overwrite
kubectl label node kind-netdevsim-worker3 node-role.kubernetes.io/worker= --overwrite