FROM gcr.io/distroless/base
COPY gcp-oci-proxy /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/gcp-oci-proxy"]