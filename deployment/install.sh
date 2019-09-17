#!/bin/bash

set -xe

if [ ! -x "$(command -v openssl)" ]; then
    echo "openssl not found"
    exit 1
elif [ ! -x "$(command -v kubectl)" ]; then
    echo "kubectl not found"
    exit 1
fi

cwd=$(dirname $0)
csr=webhook-csr
service=webhook-svc
secret=webhook-certs
namespace=kube-system

# clean-up any previously created resources for our service. Ignore errors if not present.
kubectl delete -f ${cwd}/webhook.yaml 2>/dev/null || true
kubectl delete -f ${cwd}/service.yaml 2>/dev/null || true
kubectl delete -f ${cwd}/deployment.yaml 2>/dev/null || true
kubectl delete -f ${cwd}/secret.yaml 2>/dev/null || true
kubectl delete -f ${cwd}/csr.yaml 2>/dev/null || true

tmpdir=$(mktemp -d)
echo "creating certs in tmpdir ${tmpdir} "

cat <<EOF > ${tmpdir}/csr.conf
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${service}
DNS.2 = ${service}.${namespace}
DNS.3 = ${service}.${namespace}.svc
EOF

openssl genrsa -out ${tmpdir}/key.pem 2048
openssl req -new -key ${tmpdir}/key.pem -subj "/CN=${service}" -out ${tmpdir}/csr.pem -config ${tmpdir}/csr.conf

# set server cert/key CSR in the csr template
sed -i "s/request: .*/request: $(cat ${tmpdir}/csr.pem | base64 | tr -d '\n')/" ${cwd}/csr.yaml

# create CSR
kubectl apply -f ${cwd}/csr.yaml

# verify CSR has been created
while true; do
    kubectl get csr ${csr}
    if [ "$?" -eq 0 ]; then
        break
    fi
done

# approve and fetch the signed certificate
kubectl certificate approve ${csr}

# verify certificate has been signed
for x in $(seq 10); do
    serverCert=$(kubectl get csr ${csr} -o jsonpath='{.status.certificate}')
    if [[ ${serverCert} != '' ]]; then
        break
    fi
    sleep 1
done
if [[ ${serverCert} == '' ]]; then
    echo "ERROR: After approving csr ${csr}, the signed certificate did not appear on the resource. Giving up after 10 attempts." >&2
    exit 1
fi

# set server cert/key in the secret
sed -i "s/cert.pem: .*/cert.pem: ${serverCert}/" ${cwd}/secret.yaml
sed -i "s/key.pem: .*/key.pem: $(cat ${tmpdir}/key.pem | base64 | tr -d '\n')/" ${cwd}/secret.yaml
rm -rf ${tmpdir}

# set server cert in the webhook configuration
sed -i "s/caBundle: .*/caBundle: ${serverCert}/" ${cwd}/webhook.yaml

# launch webhook service
kubectl apply -f ${cwd}/secret.yaml
kubectl apply -f ${cwd}/service.yaml
kubectl apply -f ${cwd}/deployment.yaml
kubectl apply -f ${cwd}/webhook.yaml
