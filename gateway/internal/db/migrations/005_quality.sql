-- Quality-based routing: "quality" sends to the connection with the best recent delivery rate for the
-- destination country; "balanced" picks the cheapest connection within 5 points of the best score.
ALTER TABLE routes DROP CONSTRAINT IF EXISTS routes_policy_check;
ALTER TABLE routes ADD CONSTRAINT routes_policy_check
    CHECK (policy IN ('priority', 'weighted', 'lcr', 'quality', 'balanced'));
