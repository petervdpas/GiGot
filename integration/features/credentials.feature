Feature: Credential vault (admin)
  As an administrator
  I want to store outbound credentials in a sealed vault
  So that GiGot can authenticate to external systems on my behalf without
  leaking secrets back through the admin API

  Scenario: Listing credentials requires a session
    Given the server is running
    When I GET "/api/admin/credentials"
    Then the response status should be 401

  Scenario: Admin can create and list a credential; secret never leaves the server
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/credentials"
    Then the JSON response "count" should be 0

    When I POST "/api/admin/credentials" with body '{"name":"github-personal","kind":"pat","secret":"ghp_abc"}'
    Then the response status should be 201
    And the JSON response "name" should be "github-personal"
    And the JSON response "kind" should be "pat"
    And the response body should not contain "ghp_abc"

    When I GET "/api/admin/credentials"
    Then the JSON response "count" should be 1
    And the response body should not contain "ghp_abc"

  Scenario: Duplicate credential name is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"dup","kind":"pat","secret":"s1"}'
    Then the response status should be 201

    When I POST "/api/admin/credentials" with body '{"name":"dup","kind":"pat","secret":"s2"}'
    Then the response status should be 409

  Scenario: PATCH rotates the secret without changing created_at
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"rotate-me","kind":"pat","secret":"old"}'
    And I save the JSON response "created_at" as "first_created"
    And I PATCH "/api/admin/credentials/rotate-me" with body '{"secret":"new"}'
    Then the response status should be 200
    And the JSON response "created_at" should equal saved "first_created"
    And the response body should not contain "new"

  Scenario: DELETE removes a credential
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"temp","kind":"pat","secret":"x"}'
    And I DELETE "/api/admin/credentials/temp"
    Then the response status should be 204

    When I GET "/api/admin/credentials/temp"
    Then the response status should be 404

  Scenario: Credentials survive a server restart
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"persistent","kind":"pat","secret":"s"}'
    And the server restarts
    And I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/credentials"
    Then the JSON response "count" should be 1
