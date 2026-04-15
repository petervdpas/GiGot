Feature: Formidable-context scaffolding on repo creation
  As an administrator creating a repo for a Formidable client
  I want to optionally seed the repo with the Formidable directory layout
  So that clients can clone a ready-to-use context without manual setup

  Scenario: Creating a repo without scaffolding leaves it empty
    Given the server is running
    When I POST "/api/repos" with body '{"name":"plain-repo"}'
    Then the response status should be 201
    And the repository "plain-repo" has no commits

  Scenario: Creating a repo with scaffolding seeds a Formidable context
    Given the server is running
    When I POST "/api/repos" with body '{"name":"context-repo","scaffold_formidable":true}'
    Then the response status should be 201
    And the repository "context-repo" has commits
    And the repository "context-repo" contains file "README.md"
    And the repository "context-repo" contains file "templates/basic.yaml"
    And the repository "context-repo" contains file "storage/.gitkeep"
    And the repository "context-repo" head commit is authored by "GiGot Scaffolder"
