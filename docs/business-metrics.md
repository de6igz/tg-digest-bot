# Business Metrics Collection

The bot now stores key business events in the `business_metrics` table inside Postgres so that the team can analyse product usage beyond technical telemetry.

## Stored Events

The following event types are recorded:

| Event | Description |
|-------|-------------|
| `user_registered` | New Telegram user profile was created in the service. The metadata contains the Telegram user id, locale, timezone (if provided) and referral information. |
| `channel_attached` | A channel was successfully attached to a user. Useful for tracking content acquisition. |
| `digest_requested` | A manual digest job was requested by a user either for all channels, a specific channel, or a set of tags. Metadata includes the job id, chat id, trigger cause and optional channel or tags. |
| `digest_scheduled` | The scheduler enqueued an automatic digest delivery for a user. Metadata captures the scheduled moment and job id. |
| `digest_built` | The service persisted the generated digest to the database, including item counts and optional overview metadata. |
| `digest_delivered` | A digest message was successfully sent to the user. Metadata includes delivery attempt number, number of items, tags or channel id, and timestamps. |

Each record also stores optional `user_id`, `channel_id`, a JSONB `metadata` payload, and `occurred_at` timestamp.

## Accessing the Data

Use standard SQL against the `business_metrics` table to analyse events. Example:

```sql
SELECT occurred_at, event, metadata
FROM business_metrics
WHERE event = 'digest_delivered'
  AND occurred_at >= now() - interval '7 days'
ORDER BY occurred_at DESC;
```

This table is append-only and can be safely queried by BI tools or exported for further analysis.
