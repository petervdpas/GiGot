Feature: Client enrollment
  As a Formidable client
  I want to enroll my NaCl public key
  So that the server can seal responses to me end-to-end

  Scenario: Enroll a new client
    Given the server is running
    When I POST "/api/clients/enroll" with body '{"client_id":"laptop-01","public_key":"bm90LXJlYWxseS1hLWtleS1idXQtdGhpcnR5dHdvLWJ5dGUh"}'
    Then the response status should be 400

  Scenario: Enroll with a valid generated key
    Given the server is running
    And a fresh client keypair "alice-key"
    When I enroll client "alice" with keypair "alice-key"
    Then the response status should be 201
    And the JSON response "client_id" should be "alice"
    And the JSON response "server_public_key" should not be empty

  Scenario: Re-enrolling the same client with same key is idempotent
    Given the server is running
    And a fresh client keypair "alice-key"
    When I enroll client "alice" with keypair "alice-key"
    And I enroll client "alice" with keypair "alice-key"
    Then the response status should be 201

  Scenario: Re-enrolling the same client with a different key is rejected
    Given the server is running
    And a fresh client keypair "alice-key"
    And a fresh client keypair "alice-key-2"
    When I enroll client "alice" with keypair "alice-key"
    And I enroll client "alice" with keypair "alice-key-2"
    Then the response status should be 409

  Scenario: Enroll requires a client_id
    Given the server is running
    When I POST "/api/clients/enroll" with body '{"public_key":"abc"}'
    Then the response status should be 400

  Scenario: Enrollment persists across server restarts
    Given the server is running
    And a fresh client keypair "alice-key"
    When I enroll client "alice" with keypair "alice-key"
    And the server restarts
    And I enroll client "alice" with keypair "alice-key"
    Then the response status should be 201
