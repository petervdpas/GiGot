Feature: Server keypair rotation
  As an operator about to flip this repo public (or any time a key is suspected leaked)
  I want to rotate the server keypair in place
  So that future clients speak to a fresh key, but existing admins and subscription keys survive

  Scenario: Rotation preserves admin login and changes the server pubkey
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I GET "/api/crypto/pubkey"
    And I save the JSON response "public_key" as "old_pubkey"
    And the server keypair is rotated
    And the server restarts
    And I GET "/api/crypto/pubkey"
    Then the JSON response "public_key" should not be empty
    And the JSON response "public_key" should differ from saved "old_pubkey"

    When I log in as admin "alice" with password "hunter2"
    Then the response status should be 200
