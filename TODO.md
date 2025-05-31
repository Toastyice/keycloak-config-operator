### TODO
- [&check;] CRD for keycloak config  
  Operator should be able to support multiple keycloak configs 
- [&check;] when KeycloakInstanceConfig is not Connected and ready don't run the other controllers!
- [?] When KeycloakInstanceConfig is not Connected should the status of dependecies change to Unkown?
- [&check;] When KeycloakInstanceConfig is not ready don't delete of clients, groups, realms should not work eg should be stuck!
- [] client secret kubernetes secret doesn't add status to client status
- [] client secret kubernetes secret doesn't get deleted if disabled via CRD and was enabled before

### known Issues
- Group parent relationships aren't fixed if changed manually


### Later:
- e2e tests
