# Unit Testing
## Validation
Very simple suite of tests to make sure the operator can deploy.  Normally no changes will be required here.
## Conformance
This testing is at a very high level, to ensure that given functionality is broken.  An example of this would be to test enabling the a reference plugin, and then test that the daemon has the expected logs from the reference plugin

# End-to-End testing
More in-depth testing to ensure the operator is functioning correctly, and ptp is actually running correctly on the cluster after deployment

# Feature development expectations 
## Validation
Normally no changes are required here.  Unless developing a new container for example.
## Conformance
Expected developers of any new features will add new conformance tests as part of submission
## End-to-End
New e2e tests should be added to do more in-depth testing for new features.  At least basic tests should be added before submitting feature, and should ensure plan is in place for more tests if required.
