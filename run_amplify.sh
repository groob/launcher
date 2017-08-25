#!/bin/bash

export GOOGLE_APPLICATION_CREDENTIALS=/Users/victor/.config/gcloud/application_default_credentials.json
amplify -hostname master-grpc.cloud.kolide.net:443 -enroll_secret "$(cat /etc/kolide/secret)"
