Feature: Server NaCl keypair
  As a Formidable client
  I want to fetch the server's public key
  So that I can seal request bodies end-to-end even through a gateway

  Scenario: Server exposes its public key
    Given the server is running
    When I GET "/api/crypto/pubkey"
    Then the response status should be 200
    And the JSON response "public_key" should not be empty

  Scenario: Server keypair persists across restarts
    Given the server is running
    When I GET "/api/crypto/pubkey"
    And I save the JSON response "public_key" as "first_key"
    And the server restarts
    And I GET "/api/crypto/pubkey"
    Then the JSON response "public_key" should equal saved "first_key"
