Feature: Sealed request/response bodies
  As a Formidable client behind an API gateway
  I want to seal my request body and unseal the response
  So that the gateway terminates TLS but still cannot read my payload

  Scenario: Plain API requests still work when no sealing headers are set
    Given the server is running
    And a regular account "alice" exists
    And a repository "addresses" exists
    When I POST "/api/auth/token" with body '{"username":"alice","repo":"addresses"}'
    Then the response status should be 201
    And the JSON response "username" should be "alice"

  Scenario: Sealed request body is unsealed and the response is sealed back
    Given the server is running
    And a regular account "alice" exists
    And a repository "addresses" exists
    And a fresh client keypair "alice-key"
    And I enroll client "alice" with keypair "alice-key"
    When client "alice" with keypair "alice-key" POSTs sealed "/api/auth/token" with body '{"username":"alice","repo":"addresses"}'
    Then the response status should be 201
    And the response content type should contain "application/vnd.gigot.sealed+b64"
    And opening the response with keypair "alice-key" gives JSON with "username" equal to "alice"

  Scenario: Sealed request from an unknown client is rejected
    Given the server is running
    And a fresh client keypair "mallory-key"
    When client "mallory" with keypair "mallory-key" POSTs sealed "/api/auth/token" with body '{"username":"x"}'
    Then the response status should be 401

  Scenario: Sealed request with garbage body is rejected as 400
    Given the server is running
    And a fresh client keypair "alice-key"
    And I enroll client "alice" with keypair "alice-key"
    When client "alice" POSTs "/api/auth/token" with raw sealed body "not-base64!!"
    Then the response status should be 400
