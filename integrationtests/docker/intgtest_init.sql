-- postgres configuration mimics helm postgres configured in ../helm/hotload-integration-tests/values.yaml
CREATE DATABASE hotload_test;
CREATE DATABASE hotload_test1;                                                                    
GRANT ALL PRIVILEGES ON DATABASE hotload_test TO admin;
GRANT ALL PRIVILEGES ON DATABASE hotload_test1 TO admin;
