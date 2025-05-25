Init: 
operator-sdk init --domain schella.network --repo github.com/toastyice/keycloak-config-operator --plugins=go/v4

operator-sdk create api --group keycloak --version v1alpha1 --kind Realm --resource --controller


# Generate code
make generate

# Generate CRDs
make manifests

# Install CRDs
make install
