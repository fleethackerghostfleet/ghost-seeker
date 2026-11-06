WITH dev AS (
  SELECT *, IFNULL(source_video, dataset_id) AS grp
  FROM `qwiklabs-gcp-02-34a870014566.ghost_fleet.images_clean_v2`
  WHERE eval_split = 'dev' AND dataset_id != 'singapore_maritime'
),
n AS (SELECT grp, COUNT(*) AS c FROM dev GROUP BY grp),
r AS (
  SELECT d.*, n.c,
         ROW_NUMBER() OVER (PARTITION BY d.grp ORDER BY d.timestamp_s, d.image_id) AS rn
  FROM dev d JOIN n USING (grp)
)
SELECT image_id, dataset_id, gcs_uri, width, height, sha256, source_video, timestamp_s, has_annotations, eval_split
FROM r
WHERE ((dataset_id = 'seaships7000' AND MOD(ABS(FARM_FINGERPRINT(image_id)), 100) < 25)
   OR (dataset_id != 'seaships7000' AND MOD(rn - 1, GREATEST(1, CAST(CEIL(c / 250) AS INT64))) = 0))
  -- Dev review only: omit SCIENCE BUZZ title cards and the studio newscaster.
  AND NOT (dataset_id = 'video_sailing_drone_inside_a_hurricane' AND timestamp_s < 11)
ORDER BY dataset_id, source_video, timestamp_s, image_id
