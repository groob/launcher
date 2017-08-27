#!/bin/bash

export GOOGLE_APPLICATION_CREDENTIALS=/Users/victor/.config/gcloud/application_default_credentials.json
chown -R root ./build
rm -rf /var/kolide/master.cloud.kolide.net && mkdir -p /var/kolide/master.cloud.kolide.net
rm -rf /tmp/fake*
./build/launcher -root_directory /var/kolide/master.cloud.kolide.net -hostname master-grpc.cloud.kolide.net:443 -enroll_secret_path /etc/kolide/secret 
