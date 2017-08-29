#!/bin/bash

export GOOGLE_APPLICATION_CREDENTIALS=/Users/victor/.config/gcloud/application_default_credentials.json
export LAUNCHER_IDENTIFIER=amplify-launcher-14
gcloud beta pubsub topics delete launcher.amplify
gcloud beta pubsub topics create launcher.amplify
gcloud beta pubsub subscriptions delete launcher.amplifyconsumer-group-1
gcloud beta pubsub subscriptions create launcher.amplifyconsumer-group-1 --topic launcher.amplify
amplify -hostname launcher.staging.kolide.com:443 -enroll_secret "$(cat /tmp/dababe)"
