"""
Patch file to modify stock footage concatenation to use random transitions.
This patch should be applied to the video_generation.py file.
"""

# In the section where stock segments are processed (around line 1520-1600),
# replace the existing stock segment processing logic with this:

def apply_stock_transition_patch():
    """
    This function shows how to modify the stock segment processing logic
    to use random transitions between stock footage segments.
    """
    pass

# ORIGINAL CODE (to be replaced):
"""
# Process stock segments and intelligently place middle clips
middle_clip_insertion_index = 0
for i in range(num_stock_segments):
    stock_data = stock_segment_results.get(i)
    if stock_data:
        stock_path, stock_dur = stock_data
        segments_to_concat_ordered.append(stock_path)
        duration_for_overlay_mapping_calc += stock_dur
        cumulative_video_time += stock_dur  # Add stock segment duration
        original_task_args = next((task for task in stock_tasks_args_list if task["segment_index"] == i), None)
        if original_task_args: 
            current_audio_offset_tracker += original_task_args["duration_needed"]
            
            # Check if this segment should be followed by a middle clip
            if original_task_args.get("followed_by_clip", False) and middle_clip_insertion_index < M:
                # Insert middle clip after this stock segment
                sorted_processed_middle_indices = sorted(processed_middle_clips_map.keys())
                if middle_clip_insertion_index < len(sorted_processed_middle_indices):
                    middle_clip_original_idx = sorted_processed_middle_indices[middle_clip_insertion_index]
                    mid_data = processed_middle_clips_map.get(middle_clip_original_idx)
                    if mid_data:
                        mid_path, mid_dur = mid_data
                        # Record middle clip timing
                        middle_start_time = cumulative_video_time
                        middle_end_time = cumulative_video_time + mid_dur
                        middle_clip_timestamps.append((middle_start_time, middle_end_time))
                        
                        segments_to_concat_ordered.append(mid_path)
                        duration_for_overlay_mapping_calc += mid_dur
                        cumulative_video_time += mid_dur  # Add middle clip duration
                        
                        # Get the corresponding audio insertion time for logging
                        audio_insertion_time = original_task_args["audio_offset"] + original_task_args["duration_needed"]
                        status_callback(f"{main_label} ✅ Aggiunto middle clip #{middle_clip_original_idx} a {audio_insertion_time:.1f}s audio: {os.path.basename(mid_path)} ({mid_dur:.2f}s) [video timing: {middle_start_time:.2f}-{middle_end_time:.2f}s]", False)
                        middle_clip_insertion_index += 1
                    else:
                        status_callback(f"{main_label} ❌ Middle clip data non trovata per indice {middle_clip_original_idx}", True)
                else:
                    status_callback(f"{main_label} ⚠️ Middle clip non disponibile per inserimento #{middle_clip_insertion_index + 1}", True)
        status_callback(f"{main_label} ⚠️ Segmento stock #{i+1} non generato o fallito.", True)
"""

# NEW CODE (replacement):
"""
# Process stock segments and intelligently place middle clips
middle_clip_insertion_index = 0

# Collect stock segments for transition-based concatenation
stock_segments_to_concat = []
stock_segments_data = []  # To keep track of stock segment data for later use

for i in range(num_stock_segments):
    stock_data = stock_segment_results.get(i)
    if stock_data:
        stock_path, stock_dur = stock_data
        stock_segments_to_concat.append(stock_path)
        stock_segments_data.append((i, stock_path, stock_dur))

# If we have stock segments, concatenate them with transitions
stock_with_transitions_path = None
if stock_segments_to_concat:
    try:
        from .video_ffmpeg import concatenate_stock_videos_with_transitions
    except ImportError:
        from video_ffmpeg import concatenate_stock_videos_with_transitions
            
    stock_with_transitions_path = os.path.join(temp_dir, f"stock_with_transitions_{uuid.uuid4().hex}.mp4")
    temp_files_to_clean_main.append(stock_with_transitions_path)
        
    success = concatenate_stock_videos_with_transitions(
        video_paths=stock_segments_to_concat,
        output_path=stock_with_transitions_path,
        status_callback=status_callback,
        transition_duration=0.3,  # Max 0.3 seconds as requested
        temp_dir=temp_dir
    )
        
    if success and os.path.exists(stock_with_transitions_path):
        status_callback(f"{main_label} ✅ Stock segments concatenated with transitions", False)
    else:
        # Fallback to regular concatenation if transition concatenation fails
        stock_with_transitions_path = None
        status_callback(f"{main_label} ⚠️ Failed to concatenate with transitions, using regular concatenation", True)

# Process segments in order, but replace stock segments with the transitioned version
stock_segment_index = 0
for i in range(num_stock_segments):
    stock_data = stock_segment_results.get(i)
    if stock_data:
        stock_path, stock_dur = stock_data
            
        # If we have a transitioned stock segment, use it instead of individual segments
        if stock_with_transitions_path and stock_segment_index == 0:
            # Use the transitioned version for the first stock segment
            segments_to_concat_ordered.append(stock_with_transitions_path)
            # Calculate total duration of all stock segments for overlay mapping
            total_stock_duration = sum(duration for _, _, duration in stock_segments_data)
            duration_for_overlay_mapping_calc += total_stock_duration
            cumulative_video_time += total_stock_duration
                
            # Skip adding individual stock segments since we're using the transitioned version
            stock_segment_index += len(stock_segments_to_concat)
        elif not stock_with_transitions_path:
            # Fallback: add individual stock segments if transition failed
            segments_to_concat_ordered.append(stock_path)
            duration_for_overlay_mapping_calc += stock_dur
            cumulative_video_time += stock_dur
            stock_segment_index += 1
        else:
            # Skip individual stock segments when we've already added the transitioned version
            stock_segment_index += 1
                
        original_task_args = next((task for task in stock_tasks_args_list if task["segment_index"] == i), None)
        if original_task_args: 
            current_audio_offset_tracker += original_task_args["duration_needed"]
            
            # Check if this segment should be followed by a middle clip
            if original_task_args.get("followed_by_clip", False) and middle_clip_insertion_index < M:
                # Insert middle clip after this stock segment
                sorted_processed_middle_indices = sorted(processed_middle_clips_map.keys())
                if middle_clip_insertion_index < len(sorted_processed_middle_indices):
                    middle_clip_original_idx = sorted_processed_middle_indices[middle_clip_insertion_index]
                    mid_data = processed_middle_clips_map.get(middle_clip_original_idx)
                    if mid_data:
                        mid_path, mid_dur = mid_data
                        # Record middle clip timing
                        middle_start_time = cumulative_video_time
                        middle_end_time = cumulative_video_time + mid_dur
                        middle_clip_timestamps.append((middle_start_time, middle_end_time))
                        
                        segments_to_concat_ordered.append(mid_path)
                        duration_for_overlay_mapping_calc += mid_dur
                        cumulative_video_time += mid_dur  # Add middle clip duration
                        
                        # Get the corresponding audio insertion time for logging
                        audio_insertion_time = original_task_args["audio_offset"] + original_task_args["duration_needed"]
                        status_callback(f"{main_label} ✅ Aggiunto middle clip #{middle_clip_original_idx} a {audio_insertion_time:.1f}s audio: {os.path.basename(mid_path)} ({mid_dur:.2f}s) [video timing: {middle_start_time:.2f}-{middle_end_time:.2f}s]", False)
                        middle_clip_insertion_index += 1
                    else:
                        status_callback(f"{main_label} ❌ Middle clip data non trovata per indice {middle_clip_original_idx}", True)
                else:
                    status_callback(f"{main_label} ⚠️ Middle clip non disponibile per inserimento #{middle_clip_insertion_index + 1}", True)
    else:
        status_callback(f"{main_label} ⚠️ Segmento stock #{i+1} non generato o fallito.", True)
"""