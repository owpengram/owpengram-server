-- The corrected pool may have issued collectibles beyond the former one-item
-- limit, so shrinking supply_total during rollback would violate durable state.
-- Keep the data correction; rolling back the application code is still safe.
SELECT 1;
