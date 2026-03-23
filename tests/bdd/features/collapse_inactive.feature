Feature: Collapse Inactive Sessions

  Sessions that have had no PTY output in the last 2 hours can be
  hidden from the sidebar via a toggle on the SESSIONS header.

  Scenario: All sessions visible when collapse mode is off
    Given collapse mode is off
    And sessions "active-1" and "stale-1" exist
    Then both "active-1" and "stale-1" appear in the sidebar
    And hidden count is 0

  Scenario: Stale sessions hidden when collapse mode is on
    Given collapse mode is on
    And session "active-2" had output 30 minutes ago
    And session "stale-2" had output 3 hours ago
    Then "active-2" appears in the sidebar
    And "stale-2" does not appear in the sidebar
    And hidden count is 1

  Scenario: Currently selected session always visible even if stale
    Given collapse mode is on
    And session "selected-stale" had output 3 hours ago
    And "selected-stale" is the selected session
    Then "selected-stale" appears in the sidebar

  Scenario: Search finds all sessions regardless of collapse mode
    Given collapse mode is on
    And session "search-active" had output 30 minutes ago
    And session "search-stale" had output 3 hours ago
    When I search for "search"
    Then both "search-active" and "search-stale" appear in results

  Scenario: Selecting a stale session via search reveals it
    Given collapse mode is on
    And session "revealed-sess" had output 3 hours ago
    When I select "revealed-sess"
    Then "revealed-sess" appears in the sidebar without searching

  Scenario: Toggling collapse off clears revealed sessions
    Given collapse mode is on
    And "revealed-clear" was previously revealed via search
    When I toggle collapse mode off
    Then the revealed sessions set is empty

  Scenario: Sidebar header shows filtered count
    Given collapse mode is on
    And 3 sessions are active and 2 sessions are stale
    Then the sidebar header shows "SESSIONS (3/5)"

  Scenario: Clicking "+N inactive" disables collapse
    Given collapse mode is on
    And there are hidden sessions
    When I click "+N inactive"
    Then collapse mode is off
    And all sessions appear in the sidebar

  Scenario: Activity persists across restart
    Given session "persist-sess" had output 3 hours ago
    And the activity time is saved to config
    When the app restarts and reconnects
    Then "persist-sess" still shows as inactive (3 hours old)

  Scenario: New sessions are always active
    Given collapse mode is on
    When I create a new session "brand-new"
    Then "brand-new" appears in the sidebar

  Scenario: Revealed sessions transfer on rename
    Given collapse mode is on
    And "old-name" was previously revealed via search
    When I rename "old-name" to "new-name"
    Then "new-name" is in the revealed set
    And "old-name" is not in the revealed set
