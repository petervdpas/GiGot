Feature: Repository API
  As a client using the GiGot API
  I want to manage repositories via HTTP
  So that I can create, list, and remove repos remotely

  Scenario: List repos when empty
    Given the server is running
    When I GET "/api/repos"
    Then the response status should be 200
    And the JSON response "count" should be 0

  Scenario: Create a repo via API
    Given the server is running
    When I POST "/api/repos" with body '{"name":"project-x"}'
    Then the response status should be 201
    And the response body should contain "project-x"

  Scenario: Create duplicate repo returns conflict
    Given the server is running
    And I POST "/api/repos" with body '{"name":"dup-repo"}'
    When I POST "/api/repos" with body '{"name":"dup-repo"}'
    Then the response status should be 409

  Scenario: Get repo details
    Given the server is running
    And I POST "/api/repos" with body '{"name":"details-test"}'
    When I GET "/api/repos/details-test"
    Then the response status should be 200
    And the JSON response "name" should be "details-test"

  Scenario: Get nonexistent repo returns 404
    Given the server is running
    When I GET "/api/repos/does-not-exist"
    Then the response status should be 404

  Scenario: Delete a repo via API
    Given the server is running
    And I POST "/api/repos" with body '{"name":"to-delete"}'
    When I DELETE "/api/repos/to-delete"
    Then the response status should be 204
    And I GET "/api/repos/to-delete"
    And the response status should be 404
