Feature: Per-repo Formidable bootstrap (subscriber)
  As a Formidable client connecting to a repo
  I want a single read that tells me whether the repo is Formidable-
  scaffolded, what scaffold version it carries, and what its template
  + storage shape looks like
  So that I can render the sidebar, template picker, and any
  scaffold-version warnings without crawling /tree myself.

  # Pairs with /context — that endpoint reports whether the repo IS
  # Formidable; this endpoint reports WHAT its Formidable shape is.
  # Read-only, repo-scope gated, ability-ungated. Non-Formidable
  # repos return 200 with marker_present=false and empty arrays so
  # clients can distinguish "not Formidable" from "doesn't exist."

  Scenario: Bootstrap on a freshly-scaffolded Formidable repo
    # The default scaffold ships templates/basic.yaml + a marker.
    # Repo creation needs admin auth; the bootstrap GET that
    # follows uses a regular subscription key to prove the path
    # works for the realistic Formidable-client shape.
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/repos" with body '{"name":"my-repo","scaffold_formidable":true}'
    Then the response status should be 201
    Given a token is issued for user "alice" with repos "my-repo"
    When I request "/api/repos/my-repo/formidable" with that token
    Then the response status should be 200
    And the response body should contain "\"marker_present\":true"
    And the response body should contain "\"version\":1"
    And the response body should contain "\"scaffolded_by\":\"gigot\""
    And the response body should contain "\"name\":\"basic\""
    And the response body should contain "\"path\":\"templates/basic.yaml\""

  Scenario: Bootstrap on a non-Formidable repo returns 200 with marker absent
    # A plain bare repo (no scaffold) is a valid GiGot use; the
    # /formidable endpoint should still answer rather than 404, so
    # a Formidable client can decide "this isn't my kind of repo."
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "plain" exists
    And a token is issued for user "alice" with repos "plain"
    When I request "/api/repos/plain/formidable" with that token
    Then the response status should be 200
    And the response body should contain "\"marker_present\":false"
    And the response body should contain "\"templates\":[]"
    And the response body should contain "\"storage\":[]"

  Scenario: A no-mirror subscriber still gets the bootstrap (read is ungated)
    # The Formidable bootstrap is informational; only repo scope
    # gates it. A regular subscriber with no special abilities can
    # render its UI off this response.
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "addresses" exists
    And a token is issued for user "alice" with repos "addresses"
    When I request "/api/repos/addresses/formidable" with that token
    Then the response status should be 200

  Scenario: Out-of-scope token is 403 on the bootstrap
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "addresses" exists
    And a repository "elsewhere" exists
    And a token is issued for user "alice" with repos "elsewhere"
    When I request "/api/repos/addresses/formidable" with that token
    Then the response status should be 403

  Scenario: Bootstrap on an unknown repo returns 404
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a token is issued for user "alice" with repos "ghost"
    When I request "/api/repos/ghost/formidable" with that token
    Then the response status should be 404

  Scenario: Records query lives under the formidable namespace
    # Sibling subroute — proves the namespace is more than just one
    # endpoint. The records DSL still works; only its path moved.
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"rq-ns"}'
    Then the response status should be 201
    And I GET "/api/repos/rq-ns/head"
    And I save the JSON response "version" as "head0"
    And I put a record "storage/addresses/oak.meta.json" in repo "rq-ns" with data '{"city":"London"}' updated "2025-01-01T00:00:00Z" and parent "${head0}"
    And the response status should be 200
    When I GET "/api/repos/rq-ns/formidable/records/addresses"
    Then the response status should be 200
    And the records response contains 1 records
