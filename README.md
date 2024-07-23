# GCP OCI Proxy

```
          env:
            - name: PORT
              value: ":8080"
            - name: REPOSITORY
              value: "my-chart-repository"
            - name: PROJECT
              value: "my-gcp-proxy"
            - name: REGION
              value: "us-central1"
            - name: GOOGLE_APPLICATION_CREDENTIALS
              value: "/var/run/secrets/google/key.json"
```
