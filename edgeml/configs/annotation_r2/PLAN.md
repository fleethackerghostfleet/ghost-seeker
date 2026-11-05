# Annotation round 2 plan (lead-authored; authoritative)
Set name: ghost_fleet_r2_1fps  (GCS: gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_r2_1fps/), split=dev for every seq.
Locked TEST sources (whole source excluded, any segment): Spotted_A_Submarine, US_Destroyer_POV_Warship_Crossing, Ship_in_the_Distance_Ian,
 Dolphin_from_far_distance_in_Bay_of_Bengal, Sailboat_on_the_horizon, Arriving_In_Norfolk_USA_3hr_uncut, Cruise_Ship_Docked_in_Ocean_with_Dingy_Boats,
 Ferry_Passing_By_On_River, SMD MVI_0797/0801. Already labelled (skip): the 9 ghost_fleet_5fps dev clips, SMD (public gt), fvessel.
Dev sources (GCS frames/<Name>_frames/sec_NNNN.jpg, 1 fps):
 short (all frames, one seq each): Cruise_Ship_POV(14) Dolphins_Pungo_Ferry_Bridge(5) NCL_Breakaway_Bomb_Cyclone(37) Pacific_Ocean_at_night_from_Cruise_Balcony(15)
   Partially_Obstructed_Balcony(17) Sailing_drone_inside_a_hurricane(49) Ship_crossing_Mariana_Trench(13) Sunrise_Somewhere_in_the_Ocean(40)
   Tarpon_Offshore(40) Whale_Watching_in_Northern_Peru(41) Argentina_to_Alaska_Cruise_Bridge_Timelapse(139)
 long (windows): 30_Days_at_Sea_Timelapse(644) Cargo_Ship_Storm_first_hour(3600) Cargo_Ship_Uncut_View(9000)
   rule: 4 windows of 60 contiguous frames at start offsets round(k*(N-60)/3), k=0..3; if any frame has mean luma (gray mean) < 60 and no
   chosen window already contains >=20 such frames, replace the window nearest to the darkest 60-frame stretch with that stretch. seq_id = <Name>_w<startsec>.
Teacher: gemini-2.5-flash (Vertex global endpoint), prompt ~/r2/gemini_prompt.txt verbatim, temperature 0, thinkingBudget 0, maxOutputTokens 4096,
 responseMimeType application/json + responseSchema; image via fileData gs:// URI. Second teacher (cross-check only): OWLv2-B/16 on CPU.
Classes: cargo tanker container fishing sailboat small_boat warship submarine ferry_passenger tug buoy aircraft person marine_animal unknown (ids 1..15).

## Teacher v2 (lead-authored, after first look at QA sheets: v1 had runaway rows of identical tiny boxes, boxes on own-ship deck cargo / harbour, full-frame night boxes)
Stage F2 (frame runaway): if a frame's Gemini response has >= 25 objects, drop all its boxes; frame_attrs teacher_status="runaway_dropped".
Stage F1 (repetition): within a frame, link boxes i~j if |h_i-h_j| <= 0.15*max(h_i,h_j) and |yc_i-yc_j| <= 0.02*imH; any connected component of size >= 6 is dropped.
Stage V (verifier): every surviving box -> gemini-2.5-flash with [full frame, crop] + ~/r2/gemini_verify_prompt.txt verbatim. Crop = box expanded by 50% of w/h each side,
  min 64x64 px, clipped, upscaled so short side >= 256 px, red 2px rectangle at the box. Same decoding params as v1; responseSchema {verdict enum[real_object,reject],
  label enum(15 classes), box_ok bool, reason string}. Keep only verdict=real_object; label := verifier label; attrs gain verifier_box_ok, verifier_reason, label_v1.
  Verifier failures after retries -> keep box, attrs verifier="error" (count them).
Then NMS/link/write as before. Keep v1 raw responses; report counts per stage per seq. Gemini confidence is uncalibrated (mostly 0.5/1.0) - do not use for filtering.
QA sample: reuse the existing qa_sample.csv frames unchanged (drawn before v2; avoids re-sampling after seeing results).
