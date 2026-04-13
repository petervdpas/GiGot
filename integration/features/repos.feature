Feature: Repository management
  As a GiGot server
  I need to manage bare git repositories
  So that Formidable clients can sync their data

  Scenario: Create a new repository
    Given an empty repo root
    When I create repository "my-templates"
    Then the repository "my-templates" should exist

  Scenario: Cannot create duplicate repository
    Given an empty repo root
    And I create repository "my-templates"
    When I try to create repository "my-templates" again
    Then it should fail with a duplicate error

  Scenario: List repositories
    Given an empty repo root
    And I create repository "alpha"
    And I create repository "beta"
    When I list all repositories
    Then there should be 2 repositories
    And the list should contain "alpha"
    And the list should contain "beta"

  Scenario: Non-git directories are ignored
    Given an empty repo root
    And I create repository "real-repo"
    And a plain directory "not-a-repo" exists in the repo root
    When I list all repositories
    Then there should be 1 repositories
    And the list should contain "real-repo"
