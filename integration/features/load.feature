Feature: Load gauge — X-GiGot-Load header + GET /api/health/load
  As Formidable (the local-first client of GiGot)
  I want to read GiGot's current load classification
  So that I can pace background sync, surface a "server busy" hint
  to the user, and skip optional mirror dispatches when the host is
  saturated. Local writes never wait on this signal — the load
  contract is purely advisory.

  GiGot exposes the gauge two ways. First, every response carries an
  `X-GiGot-Load` header so a client gets the value as a free
  side-effect of normal traffic. Second, a dedicated public endpoint
  returns the full snapshot for explicit polls (Azure Monitor, ops
  dashboards, debugging).

  Both surfaces report the same `level` value — the dedicated endpoint
  just exposes the raw signals (in_flight count, p95 / p99 of recent
  durations, sample-window count) alongside it.

  Scenario: Idle server reports low load on the dedicated endpoint
    Given the server is running
    When I GET "/api/health/load"
    Then the response status should be 200
    And the JSON response "level" should be "low"
    And the JSON response "in_flight" should be 0

  Scenario: Every response carries the X-GiGot-Load header
    Given the server is running
    When I GET "/api/health"
    Then the response status should be 200
    And the response header "X-GiGot-Load" should be one of "low,medium,high"

  Scenario: Header rides on 404 responses too so clients always get a read
    Given the server is running
    When I GET "/this-endpoint-does-not-exist"
    Then the response status should be 404
    And the response header "X-GiGot-Load" should be one of "low,medium,high"

  Scenario: Load endpoint rejects non-GET methods
    Given the server is running
    When I POST "/api/health/load" with body '{}'
    Then the response status should be 405

  Scenario: Load endpoint is public — no session required
    Given the server is running
    When I GET "/api/health/load"
    Then the response status should be 200
    And the JSON response "level" should be "low"

  Scenario: Load endpoint also reports push slot capacity
    Given the server is running
    When I GET "/api/health/load"
    Then the response status should be 200
    And the JSON response "push_slot_capacity" should be 10
    And the JSON response "push_slot_in_use" should be 0

